/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package drbdr

import (
	"context"
	"sync"
	"testing"
	"time"
)

func readyCache(t *testing.T) *DRBDPortCache {
	t.Helper()
	c := NewDRBDPortCache()
	c.BeginDump()
	c.EndDump()
	return c
}

// --- Data correctness ---

func TestDRBDPortCache_Add_GetUsedPorts(t *testing.T) {
	c := readyCache(t)

	c.Add("r0", "10.0.0.1", 7000)
	c.Add("r0", "10.0.0.1", 7001)
	c.Add("r1", "10.0.0.1", 7002)
	c.Add("r1", "10.0.0.2", 7000)

	ports, err := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 3 {
		t.Fatalf("expected 3 ports for 10.0.0.1, got %d", len(ports))
	}
	for _, p := range []uint{7000, 7001, 7002} {
		if _, ok := ports[p]; !ok {
			t.Errorf("expected port %d in result", p)
		}
	}

	ports2, err := c.GetUsedPorts(context.Background(), "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(ports2) != 1 {
		t.Fatalf("expected 1 port for 10.0.0.2, got %d", len(ports2))
	}
	if _, ok := ports2[7000]; !ok {
		t.Error("expected port 7000 for 10.0.0.2")
	}

	empty, err := c.GetUsedPorts(context.Background(), "10.0.0.99")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 ports for unknown IP, got %d", len(empty))
	}
}

func TestDRBDPortCache_Remove(t *testing.T) {
	c := readyCache(t)

	c.Add("r0", "10.0.0.1", 7000)
	c.Add("r0", "10.0.0.1", 7001)
	c.Remove("r0", "10.0.0.1", 7000)

	ports, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if _, ok := ports[7000]; ok {
		t.Error("port 7000 should have been removed")
	}
	if _, ok := ports[7001]; !ok {
		t.Error("port 7001 should still be present")
	}
}

func TestDRBDPortCache_RemoveResource(t *testing.T) {
	c := readyCache(t)

	c.Add("r0", "10.0.0.1", 7000)
	c.Add("r0", "10.0.0.1", 7001)
	c.Add("r0", "10.0.0.2", 7000)
	c.Add("r1", "10.0.0.1", 7002)

	c.RemoveResource("r0")

	ports1, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if len(ports1) != 1 {
		t.Fatalf("expected 1 port for 10.0.0.1 after RemoveResource, got %d", len(ports1))
	}
	if _, ok := ports1[7002]; !ok {
		t.Error("port 7002 (from r1) should still be present")
	}

	ports2, _ := c.GetUsedPorts(context.Background(), "10.0.0.2")
	if len(ports2) != 0 {
		t.Fatalf("expected 0 ports for 10.0.0.2 after RemoveResource, got %d", len(ports2))
	}
}

func TestDRBDPortCache_Add_Idempotent(t *testing.T) {
	c := readyCache(t)

	c.Add("r0", "10.0.0.1", 7000)
	c.Add("r0", "10.0.0.1", 7000)

	ports, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if len(ports) != 1 {
		t.Fatalf("expected 1 port after duplicate Add, got %d", len(ports))
	}
}

func TestDRBDPortCache_Remove_NonExistent(t *testing.T) {
	c := readyCache(t)

	c.Remove("r0", "10.0.0.1", 7000)
	c.RemoveResource("r99")
}

func TestDRBDPortCache_GetUsedPorts_ReturnsCopy(t *testing.T) {
	c := readyCache(t)
	c.Add("r0", "10.0.0.1", 7000)

	ports, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	ports[9999] = struct{}{}

	ports2, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if _, ok := ports2[9999]; ok {
		t.Error("mutation of returned map should not affect cache")
	}
}

// --- Blocking ---

func TestDRBDPortCache_WaitReady_BlocksUntilEndDump(t *testing.T) {
	c := NewDRBDPortCache()

	ready := make(chan struct{})
	go func() {
		if err := c.WaitReady(context.Background()); err != nil {
			t.Errorf("WaitReady: %v", err)
		}
		close(ready)
	}()

	select {
	case <-ready:
		t.Fatal("WaitReady should block before EndDump")
	case <-time.After(50 * time.Millisecond):
	}

	c.BeginDump()
	c.EndDump()

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("WaitReady should have unblocked after EndDump")
	}
}

func TestDRBDPortCache_WaitReady_ContextCancelled(t *testing.T) {
	c := NewDRBDPortCache()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.WaitReady(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDRBDPortCache_GetUsedPorts_BlocksDuringDump(t *testing.T) {
	c := readyCache(t)
	c.Add("r0", "10.0.0.1", 7000)

	c.BeginDump()

	done := make(chan struct{})
	go func() {
		_, _ = c.GetUsedPorts(context.Background(), "10.0.0.1")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("GetUsedPorts should block during dump")
	case <-time.After(50 * time.Millisecond):
	}

	c.Add("r1", "10.0.0.1", 7099)
	c.EndDump()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("GetUsedPorts should have unblocked after EndDump")
	}
}

func TestDRBDPortCache_GetUsedPorts_ConcurrentReaders(t *testing.T) {
	c := readyCache(t)
	c.Add("r0", "10.0.0.1", 7000)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ports, err := c.GetUsedPorts(context.Background(), "10.0.0.1")
			if err != nil {
				t.Errorf("GetUsedPorts: %v", err)
			}
			if len(ports) != 1 {
				t.Errorf("expected 1 port, got %d", len(ports))
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent readers should not deadlock")
	}
}

// --- BeginDump / EndDump lifecycle ---

func TestDRBDPortCache_BeginDump_ClearsData(t *testing.T) {
	c := readyCache(t)
	c.Add("r0", "10.0.0.1", 7000)

	c.BeginDump()
	c.EndDump()

	ports, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports after BeginDump cleared data, got %d", len(ports))
	}
}

func TestDRBDPortCache_Reconnect(t *testing.T) {
	c := NewDRBDPortCache()

	// First dump
	c.BeginDump()
	c.Add("r0", "10.0.0.1", 7000)
	c.EndDump()

	ports, _ := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if len(ports) != 1 {
		t.Fatalf("expected 1 port after first dump, got %d", len(ports))
	}

	// Live update
	c.Add("r1", "10.0.0.1", 7001)

	// Reconnect: second dump clears old data
	c.BeginDump()
	c.Add("r2", "10.0.0.1", 7050)
	c.EndDump()

	ports, _ = c.GetUsedPorts(context.Background(), "10.0.0.1")
	if len(ports) != 1 {
		t.Fatalf("expected 1 port after reconnect dump, got %d", len(ports))
	}
	if _, ok := ports[7050]; !ok {
		t.Error("expected port 7050 from second dump")
	}
	if _, ok := ports[7000]; ok {
		t.Error("port 7000 from first dump should have been cleared")
	}
}

// --- AbortDump ---

func TestDRBDPortCache_AbortDump_UnlocksDumpMu(t *testing.T) {
	c := readyCache(t)
	c.Add("r0", "10.0.0.1", 7000)

	c.BeginDump()
	c.AbortDump()

	// GetUsedPorts should not deadlock (dumpMu is unlocked).
	// Data was cleared by BeginDump.
	ports, err := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 0 {
		t.Errorf("expected 0 ports after BeginDump+AbortDump, got %d", len(ports))
	}
}

func TestDRBDPortCache_AbortDump_DoesNotMarkReady(t *testing.T) {
	c := NewDRBDPortCache()

	c.BeginDump()
	c.AbortDump()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.WaitReady(ctx)
	if err == nil {
		t.Fatal("WaitReady should still block after AbortDump (readyCh not closed)")
	}
}

func TestDRBDPortCache_AbortDump_ThenBeginDumpEndDump(t *testing.T) {
	c := NewDRBDPortCache()

	// First attempt: fails before exists -
	c.BeginDump()
	c.Add("r0", "10.0.0.1", 7000)
	c.AbortDump()

	// Second attempt: succeeds
	c.BeginDump()
	c.Add("r1", "10.0.0.1", 7050)
	c.EndDump()

	ports, err := c.GetUsedPorts(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port after retry, got %d", len(ports))
	}
	if _, ok := ports[7050]; !ok {
		t.Error("expected port 7050 from second dump")
	}
}

// --- parseLocalAddr ---

func TestParseLocalAddr_Valid(t *testing.T) {
	tests := []struct {
		input    string
		wantIP   string
		wantPort uint
	}{
		{"ipv4:10.12.1.19:7096", "10.12.1.19", 7096},
		{"ipv4:192.168.0.1:7000", "192.168.0.1", 7000},
		{"ipv4:10.0.0.1:7999", "10.0.0.1", 7999},
	}
	for _, tt := range tests {
		ip, port, ok := parseLocalAddr(tt.input)
		if !ok {
			t.Errorf("parseLocalAddr(%q) returned ok=false", tt.input)
			continue
		}
		if ip != tt.wantIP || port != tt.wantPort {
			t.Errorf("parseLocalAddr(%q) = (%q, %d), want (%q, %d)", tt.input, ip, port, tt.wantIP, tt.wantPort)
		}
	}
}

func TestParseLocalAddr_Invalid(t *testing.T) {
	for _, input := range []string{
		"",
		"garbage",
		"ipv4:10.0.0.1",
		"ipv4:10.0.0.1:abc",
		"ipv4:10.0.0.1:-1",
	} {
		_, _, ok := parseLocalAddr(input)
		if ok {
			t.Errorf("parseLocalAddr(%q) should have returned ok=false", input)
		}
	}
}
