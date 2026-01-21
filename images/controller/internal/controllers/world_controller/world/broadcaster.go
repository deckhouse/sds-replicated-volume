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

package world

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// broadcaster fans out values to multiple subscribers.
type broadcaster[T any] struct {
	mu   sync.RWMutex
	subs map[chan T]struct{}
}

// newBroadcaster creates a new broadcaster.
func newBroadcaster[T any]() *broadcaster[T] {
	return &broadcaster[T]{
		subs: make(map[chan T]struct{}),
	}
}

// subscribe creates a new subscription channel with the given buffer size.
func (b *broadcaster[T]) subscribe(buf int) <-chan T {
	c := make(chan T, buf)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c
}

// publish sends a value to all subscribers. Blocks if any subscriber's buffer is full.
func (b *broadcaster[T]) publish(ctx context.Context, v T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- v:
			// sent without blocking
		default:
			log.FromContext(ctx).Info("subscriber channel full, waiting")
			ch <- v
		}
	}
}
