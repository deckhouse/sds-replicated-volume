package drbd

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

var DefaultPortCache = NewPortCache(context.Background(), PortRangeMin, PortRangeMax)

type PortCache struct {
	ctx            context.Context
	mu             sync.Mutex
	minPort        uint
	maxPort        uint
	allocatorsByIP map[string]portAllocator
}

func NewPortCache(ctx context.Context, minPort, maxPort uint) *PortCache {
	return &PortCache{
		ctx:            ctx,
		minPort:        minPort,
		maxPort:        maxPort,
		allocatorsByIP: map[string]portAllocator{},
	}
}

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
