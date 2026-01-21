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
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/event"

	wrld "github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
)

type World struct {
	initMu          sync.Mutex
	initialized     bool
	initializedChan chan struct{}

	worldBroadcaster *broadcaster[event.TypedGenericEvent[wrld.WorldKey]]
	nodesBroadcaster *broadcaster[event.TypedGenericEvent[wrld.NodeName]]
}

func NewWorld() *World {
	return &World{
		initializedChan:  make(chan struct{}),
		worldBroadcaster: newBroadcaster[event.TypedGenericEvent[wrld.WorldKey]](),
		nodesBroadcaster: newBroadcaster[event.TypedGenericEvent[wrld.NodeName]](),
	}
}

// Initialize runs the initialization function once.
// The function receives setTheWorld callback. If fn calls setTheWorld(),
// initialization is marked as complete and won't run again.
// If fn doesn't call setTheWorld(), initialization will be retried on the next call.
func (w *World) Initialize(fn func(setTheWorld func())) {
	w.initMu.Lock()
	defer w.initMu.Unlock()

	if w.initialized {
		return
	}

	fn(w.setTheWorld)
}

func (w *World) setTheWorld() { // TODO there will be args with the world state
	w.initialized = true
	close(w.initializedChan)
}

func (w *World) GetGate() wrld.WorldGate {
	return worldGate{w: w}
}

func (w *World) GetBus() wrld.WorldBus {
	return &worldBus{w: w}
}

type worldGate struct {
	w *World
}

// GetWorldView returns a WorldView for the given RSC configuration.
// Blocks until World is initialized.
// Returns an error if the context is cancelled before initialization completes.
func (g worldGate) GetWorldView(ctx context.Context, key wrld.WorldKey) (wrld.WorldView, error) {
	select {
	case <-g.w.initializedChan:

		// TODO: implement
		_ = key

		return wrld.WorldView{}, nil
	case <-ctx.Done():
		return wrld.WorldView{}, fmt.Errorf("waiting for world initialization: %w", ctx.Err())
	}
}

type worldBus struct {
	w *World
}

func (b *worldBus) WorldSource() <-chan event.TypedGenericEvent[wrld.WorldKey] {
	return b.w.worldBroadcaster.subscribe(100)
}

func (b *worldBus) NodeSource() <-chan event.TypedGenericEvent[wrld.NodeName] {
	return b.w.nodesBroadcaster.subscribe(100)
}
