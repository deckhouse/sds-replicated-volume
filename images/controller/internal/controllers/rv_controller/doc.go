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

// Package rvcontroller implements the rv_controller controller, which manages ReplicatedVolume
// metadata (labels).
//
// # Controller Responsibilities
//
// The controller ensures that the ReplicatedStorageClass label is set on each ReplicatedVolume
// to match spec.replicatedStorageClassName.
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To reconcile metadata
//
// # Triggers
//
// The controller reconciles when:
//   - RV create/update (idempotent; label set only if missing or mismatched)
package rvcontroller
