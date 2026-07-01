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
	"strconv"
	"strings"
	"sync"
)

type ipPort struct {
	ip   string
	port uint
}

// DRBDPortCache tracks ports configured in the DRBD kernel, maintained by the
// Scanner from events2 path events and read by the PortRegistry during
// allocation.
//
// Concurrency model:
//   - Scanner goroutine is the sole writer (BeginDump/EndDump/Add/Remove/RemoveResource).
//   - PortRegistry goroutines are readers (WaitReady/GetUsedPorts).
//   - Two independent blocking gates protect readers:
//     1. readyCh — blocks until the first initial dump completes (one-time).
//     2. dumpMu  — blocks during any dump (initial or reconnect).
type DRBDPortCache struct {
	readyCh   chan struct{}
	readyOnce sync.Once

	// Scanner holds Write lock during dump; readers hold Read lock.
	dumpMu sync.RWMutex

	dataMu sync.RWMutex
	byRes  map[string]map[ipPort]struct{}
	byIP   map[string]map[uint]struct{}
}

func NewDRBDPortCache() *DRBDPortCache {
	return &DRBDPortCache{
		readyCh: make(chan struct{}),
		byRes:   make(map[string]map[ipPort]struct{}),
		byIP:    make(map[string]map[uint]struct{}),
	}
}

// BeginDump prepares for a new events2 initial dump. It blocks all readers
// (via dumpMu) and clears existing data. Must be paired with EndDump.
func (c *DRBDPortCache) BeginDump() {
	c.dumpMu.Lock()
	c.dataMu.Lock()
	c.byRes = make(map[string]map[ipPort]struct{})
	c.byIP = make(map[string]map[uint]struct{})
	c.dataMu.Unlock()
}

// EndDump signals that the events2 dump is complete. It unblocks readers and,
// on the first call, marks the cache as ready (unblocking WaitReady callers).
func (c *DRBDPortCache) EndDump() {
	c.readyOnce.Do(func() { close(c.readyCh) })
	c.dumpMu.Unlock()
}

// AbortDump releases the dump lock without marking the cache as ready.
// Used when events2 fails before the initial dump completes ("exists -" not
// seen). On first startup this keeps WaitReady blocking; on reconnect this
// briefly exposes empty data until the next BeginDump.
func (c *DRBDPortCache) AbortDump() {
	c.dumpMu.Unlock()
}

// Add records a port for a DRBD resource. Called on "exists path" and
// "create path" events. Idempotent.
func (c *DRBDPortCache) Add(resourceName, ip string, port uint) {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()

	key := ipPort{ip: ip, port: port}

	resSet := c.byRes[resourceName]
	if resSet == nil {
		resSet = make(map[ipPort]struct{})
		c.byRes[resourceName] = resSet
	}
	resSet[key] = struct{}{}

	ipSet := c.byIP[ip]
	if ipSet == nil {
		ipSet = make(map[uint]struct{})
		c.byIP[ip] = ipSet
	}
	ipSet[port] = struct{}{}
}

// Remove deletes a specific port for a DRBD resource. Called on "destroy path"
// events. Idempotent.
func (c *DRBDPortCache) Remove(resourceName, ip string, port uint) {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()

	key := ipPort{ip: ip, port: port}

	if resSet := c.byRes[resourceName]; resSet != nil {
		delete(resSet, key)
		if len(resSet) == 0 {
			delete(c.byRes, resourceName)
		}
	}

	if ipSet := c.byIP[ip]; ipSet != nil {
		delete(ipSet, port)
		if len(ipSet) == 0 {
			delete(c.byIP, ip)
		}
	}
}

// RemoveResource deletes all ports for a DRBD resource. Called on
// "destroy resource" events as a safety net (individual paths are normally
// removed via Remove before this).
func (c *DRBDPortCache) RemoveResource(resourceName string) {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()

	for key := range c.byRes[resourceName] {
		if ipSet := c.byIP[key.ip]; ipSet != nil {
			delete(ipSet, key.port)
			if len(ipSet) == 0 {
				delete(c.byIP, key.ip)
			}
		}
	}
	delete(c.byRes, resourceName)
}

// WaitReady blocks until the first events2 initial dump completes (EndDump is
// called). Returns ctx.Err() if the context is cancelled before that.
func (c *DRBDPortCache) WaitReady(ctx context.Context) error {
	select {
	case <-c.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetUsedPorts returns a copy of the port set for the given IP. It blocks
// until the cache is ready and until any ongoing dump completes.
func (c *DRBDPortCache) GetUsedPorts(ctx context.Context, ip string) (map[uint]struct{}, error) {
	select {
	case <-c.readyCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.dumpMu.RLock()
	defer c.dumpMu.RUnlock()

	c.dataMu.RLock()
	defer c.dataMu.RUnlock()

	src := c.byIP[ip]
	result := make(map[uint]struct{}, len(src))
	for p := range src {
		result[p] = struct{}{}
	}
	return result, nil
}

// parseLocalAddr extracts IP and port from the events2 path "local" field.
// Format: "ipv4:10.12.1.19:7096" → ("10.12.1.19", 7096, true).
func parseLocalAddr(local string) (ip string, port uint, ok bool) {
	parts := strings.SplitN(local, ":", 3)
	if len(parts) != 3 {
		return "", 0, false
	}
	p, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return "", 0, false
	}
	return parts[1], uint(p), true
}
