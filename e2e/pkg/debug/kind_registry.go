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

package debug

import "sync"

// KindRegistry maps full Kubernetes Kind names to kubectl short names and
// vice versa. Safe for concurrent use.
type KindRegistry struct {
	mu          sync.RWMutex
	shortToFull map[string]string
	fullToShort map[string]string
}

// NewKindRegistry creates an empty KindRegistry.
func NewKindRegistry() *KindRegistry {
	return &KindRegistry{
		shortToFull: map[string]string{},
		fullToShort: map[string]string{},
	}
}

// Register records a bidirectional mapping between a kubectl short name
// and the full Kubernetes Kind.
func (r *KindRegistry) Register(shortKind, fullKind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shortToFull[shortKind] = fullKind
	r.fullToShort[fullKind] = shortKind
}

// ShortFor returns the kubectl short name for a full Kubernetes Kind
// (e.g. "ReplicatedStorageClass" → "rsc"). Returns the input unchanged if
// no mapping is registered.
func (r *KindRegistry) ShortFor(fullKind string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.fullToShort[fullKind]; ok {
		return s
	}
	return fullKind
}

// FullFor returns the full Kubernetes Kind for a kubectl short name
// (e.g. "rsc" → "ReplicatedStorageClass"). Returns the input unchanged if
// no mapping is registered.
func (r *KindRegistry) FullFor(shortKind string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if f, ok := r.shortToFull[shortKind]; ok {
		return f
	}
	return shortKind
}

// defaultKindRegistry is the package-level registry used by global convenience
// functions. CLI consumers (cmd/main.go) use this via RegisterKind/ShortKindFor/FullKindFor.
var defaultKindRegistry = NewKindRegistry()

// RegisterKind records a mapping on the default package-level registry.
func RegisterKind(shortKind, fullKind string) {
	defaultKindRegistry.Register(shortKind, fullKind)
}

// ShortKindFor returns the short name from the default package-level registry.
func ShortKindFor(fullKind string) string {
	return defaultKindRegistry.ShortFor(fullKind)
}

// FullKindFor returns the full kind from the default package-level registry.
func FullKindFor(shortKind string) string {
	return defaultKindRegistry.FullFor(shortKind)
}
