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

package testhelpers

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
)

// FakeWorldGate is a test implementation of world.WorldGate.
type FakeWorldGate struct {
	Views map[world.WorldKey]world.WorldView
}

var _ world.WorldGate = (*FakeWorldGate)(nil)

// NewFakeWorldGate creates a new FakeWorldGate.
func NewFakeWorldGate() *FakeWorldGate {
	return &FakeWorldGate{
		Views: make(map[world.WorldKey]world.WorldView),
	}
}

// GetWorldView returns the WorldView for the given key from the map.
func (f *FakeWorldGate) GetWorldView(ctx context.Context, key world.WorldKey) (world.WorldView, error) {
	if view, ok := f.Views[key]; ok {
		return view, nil
	}
	return world.WorldView{}, fmt.Errorf("WorldView not found for key: %+v", key)
}
