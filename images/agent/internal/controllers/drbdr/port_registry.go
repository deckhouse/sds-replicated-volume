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
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
)

type pendingEntry struct {
	port      uint
	allocTime time.Time
}

// PortRegistry allocates DRBD ports by combining four sources:
//   - S1: DRBD kernel state (DRBDPortCache, from events2)
//   - S2: K8s DRBDResource cache (status.addresses)
//   - S3: OS socket validation (net.Listen without SO_REUSEADDR)
//   - S4: In-process pending allocations (with TTL)
type PortRegistry struct {
	mu  sync.Mutex
	min uint
	max uint

	pending map[string][]pendingEntry
	ttl     time.Duration

	cl       client.Reader
	nodeName string

	drbdPorts *DRBDPortCache

	listenCfg net.ListenConfig
}

// cleanExpiredPending removes pending entries older than TTL for the given IP.
// Must be called under r.mu.
func (r *PortRegistry) cleanExpiredPending(ip string) {
	entries := r.pending[ip]
	now := time.Now()
	n := 0
	for _, e := range entries {
		if now.Sub(e.allocTime) <= r.ttl {
			entries[n] = e
			n++
		}
	}
	if n == 0 {
		delete(r.pending, ip)
	} else {
		r.pending[ip] = entries[:n]
	}
}

// collectK8sPorts lists DRBDResources on this node from the informer cache
// and returns the set of ports allocated to the given IP.
func (r *PortRegistry) collectK8sPorts(ctx context.Context, ip string) (map[uint]struct{}, error) {
	var list v1alpha1.DRBDResourceList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldDRBDRByNodeName: r.nodeName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, fmt.Errorf("listing DRBDResources on node %s: %w", r.nodeName, err)
	}

	result := make(map[uint]struct{})
	for i := range list.Items {
		for j := range list.Items[i].Status.Addresses {
			addr := &list.Items[i].Status.Addresses[j].Address
			if addr.IPv4 == ip && addr.Port != 0 {
				result[addr.Port] = struct{}{}
			}
		}
	}
	return result, nil
}

func NewPortRegistry(
	cl client.Reader,
	nodeName string,
	drbdPorts *DRBDPortCache,
	minPort, maxPort uint,
	pendingTTL time.Duration,
) *PortRegistry {
	return &PortRegistry{
		min:       minPort,
		max:       maxPort,
		pending:   make(map[string][]pendingEntry),
		ttl:       pendingTTL,
		cl:        cl,
		nodeName:  nodeName,
		drbdPorts: drbdPorts,
		listenCfg: net.ListenConfig{
			Control: func(_, _ string, c syscall.RawConn) error {
				var sErr error
				err := c.Control(func(fd uintptr) {
					sErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
				})
				if err != nil {
					return err
				}
				return sErr
			},
		},
	}
}

// Allocate returns an available port for the given IP address. It blocks until
// the DRBD port cache is ready (first events2 dump complete) and until any
// ongoing dump finishes.
func (r *PortRegistry) Allocate(ctx context.Context, ip string) (uint, error) {
	// 1. Wait for S1 readiness (blocks until first events2 dump).
	if err := r.drbdPorts.WaitReady(ctx); err != nil {
		return 0, fmt.Errorf("waiting for DRBD port cache: %w", err)
	}

	// 2. S1: DRBD kernel ports (may block during redump — no mu held).
	s1Ports, err := r.drbdPorts.GetUsedPorts(ctx, ip)
	if err != nil {
		return 0, fmt.Errorf("getting DRBD kernel ports: %w", err)
	}

	// 3. S2: K8s DRBDResource cache (thread-safe, no mu held).
	s2Ports, err := r.collectK8sPorts(ctx, ip)
	if err != nil {
		return 0, fmt.Errorf("collecting K8s ports: %w", err)
	}

	// 4. Take lock for S4 access and port probing.
	r.mu.Lock()
	defer r.mu.Unlock()

	// 5. Clean expired pending entries.
	r.cleanExpiredPending(ip)

	// 6. Build usedPorts = S1 ∪ S2 ∪ S4.
	usedPorts := make(map[uint]struct{}, len(s1Ports)+len(s2Ports)+len(r.pending[ip]))
	for p := range s1Ports {
		usedPorts[p] = struct{}{}
	}
	for p := range s2Ports {
		usedPorts[p] = struct{}{}
	}
	for _, e := range r.pending[ip] {
		usedPorts[e.port] = struct{}{}
	}

	// 7. Find a free port.
	for p := r.min; p <= r.max; p++ {
		if _, used := usedPorts[p]; used {
			continue
		}
		l, listenErr := r.listenCfg.Listen(ctx, "tcp", fmt.Sprintf("%s:%d", ip, p))
		if listenErr != nil {
			continue
		}
		l.Close()

		r.pending[ip] = append(r.pending[ip], pendingEntry{port: p, allocTime: time.Now()})
		return p, nil
	}

	return 0, fmt.Errorf("no free port in range %d-%d for IP %s", r.min, r.max, ip)
}
