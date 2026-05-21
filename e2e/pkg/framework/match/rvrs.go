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

package match

import (
	"fmt"

	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// RVRS is the namespace for ReplicatedVolumeReplicaSnapshot-specific matchers.
var RVRS rvrs

type rvrs struct{}

func asRVRS(obj client.Object) *v1alpha1.ReplicatedVolumeReplicaSnapshot {
	r, ok := obj.(*v1alpha1.ReplicatedVolumeReplicaSnapshot)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.ReplicatedVolumeReplicaSnapshot, got %T", obj))
	}
	return r
}

// PhaseIs matches when RVRS.Status.Phase equals p.
func (rvrs) PhaseIs(p v1alpha1.ReplicatedVolumeReplicaSnapshotPhase) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVRS(obj)
		if r.Status.Phase == p {
			return true, fmt.Sprintf("phase=%s", r.Status.Phase)
		}
		return false, fmt.Sprintf("phase=%q, expected %q", r.Status.Phase, p)
	})
}

// ReadyToUse matches when RVRS.Status.ReadyToUse is true.
func (rvrs) ReadyToUse() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVRS(obj)
		if r.Status.ReadyToUse {
			return true, "readyToUse=true"
		}
		return false, fmt.Sprintf("readyToUse=false (phase=%q)", r.Status.Phase)
	})
}

// SnapshotHandleSet matches when RVRS.Status.SnapshotHandle is non-empty.
func (rvrs) SnapshotHandleSet() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVRS(obj)
		if r.Status.SnapshotHandle != "" {
			return true, fmt.Sprintf("snapshotHandle=%q", r.Status.SnapshotHandle)
		}
		return false, "snapshotHandle is empty"
	})
}

// Custom creates a matcher with a typed function for ReplicatedVolumeReplicaSnapshot.
func (rvrs) Custom(name string, fn func(*v1alpha1.ReplicatedVolumeReplicaSnapshot) bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVRS(obj)
		if fn(r) {
			return true, name + ": matched"
		}
		return false, name + ": not matched"
	})
}
