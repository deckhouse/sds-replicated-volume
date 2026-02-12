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
)

const (
	PortRangeMin = uint(7000)
	PortRangeMax = uint(7999)
)

// PortCache maintains port allocators for different IP addresses.
// It is designed to be owned by the reconciler and passed to helpers as a delegate.
//
// Note: The PortCache maintains port allocation across reconciliations to ensure
// stable port assignments. This is acceptable because the cache is deterministic
// relative to its state and produces stable outputs for the same inputs.
type PortCache struct {
	ctx            context.Context
	mu             sync.Mutex
	minPort        uint
	maxPort        uint
	allocatorsByIP map[string]portAllocator
}

// NewPortCache creates a new PortCache with the given port range.
func NewPortCache(ctx context.Context, minPort, maxPort uint) *PortCache {
	return &PortCache{
		ctx:            ctx,
		minPort:        minPort,
		maxPort:        maxPort,
		allocatorsByIP: map[string]portAllocator{},
	}
}

// Allocate returns an available port for the given IP address.
// If the IP address has not been seen before, a new allocator is created.
// Returns 0 if no port is available.
func (pc *PortCache) Allocate(ip string) uint {
	pc.mu.Lock()
	alloc, ok := pc.allocatorsByIP[ip]
	if !ok {
		alloc = newPortAllocator(pc.ctx, ip, pc.minPort, pc.maxPort)
		pc.allocatorsByIP[ip] = alloc
	}
	pc.mu.Unlock()

	if release := <-alloc; release != nil {
		return release()
	}
	return 0
}

type portAllocator <-chan func() uint

func newPortAllocator(ctx context.Context, ip string, minPort, maxPort uint) portAllocator {
	ch := make(chan func() uint)
	go func() {
		defer close(ch)
		for p := minPort; ctx.Err() == nil; p++ {
			if p > maxPort {
				p = minPort
			}
			l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", ip, p))
			if err != nil {
				continue
			}
			select {
			case ch <- func() uint { l.Close(); return p }:
			case <-ctx.Done():
				l.Close()
			}
		}
	}()
	return ch
}
