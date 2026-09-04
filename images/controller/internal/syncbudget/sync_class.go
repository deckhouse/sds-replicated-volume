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

// Package syncbudget decides which volumes may start a resync and which must wait,
// so that no node is asked to serve more resync ends than it can carry.
//
// Everything here is pure: no Kubernetes client, no DeepCopy, no time, no randomness.
// The package holds no state between calls — occupancy is derived from the observed
// state of replicas, and queue order from a total sort.
package syncbudget

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Class is the priority of a volume's claim on a resync slot.
//
// The ladder is the declaration order below: a smaller Class outranks a larger one.
type Class uint8

const (
	// ClassIOBlocked is a volume with no UpToDate copy left, so its IO is stopped.
	ClassIOBlocked Class = iota
	// ClassDeficit is a volume holding fewer UpToDate copies than it is owed. A lost copy and a
	// freshly widened configuration are one state here: by the numbers they are identical.
	ClassDeficit
	// ClassNone is a volume that needs no resync slot. It is not the lowest priority:
	// a ClassNone volume never enters the queue at all.
	ClassNone
)

// String names the class for a log line. A value outside the ladder is named rather than left
// blank, so a bug upstream cannot produce an empty name.
func (c Class) String() string {
	switch c {
	case ClassIOBlocked:
		return "IOBlocked"
	case ClassDeficit:
		return "Deficit"
	case ClassNone:
		return "None"
	default:
		return "Unknown"
	}
}

// ComputeActualSyncClass reports the priority of rv's claim on a resync slot.
//
// It reads only rv: the count it is owed comes from status.configuration and the count
// it holds from status.effectiveLayout, so no replica list is needed.
func ComputeActualSyncClass(rv *v1alpha1.ReplicatedVolume) Class {
	short, ok := computeActualCopiesShort(rv)
	if !ok {
		return ClassNone
	}

	// 1. Effective GMDR below zero means not one UpToDate copy is left and the
	// volume's IO is stopped. Nothing outranks that.
	if gmdr := rv.Status.EffectiveLayout.GuaranteedMinimumDataRedundancy; gmdr != nil && *gmdr < 0 {
		return ClassIOBlocked
	}

	// 2. A volume short of copies is in deficit, however it got there.
	if short > 0 {
		return ClassDeficit
	}

	return ClassNone
}

// computeActualCopiesShort reports how many UpToDate copies rv is short of, and false when the
// question cannot be asked at all because rv has no resolved configuration and therefore no D.
//
// Both the class ladder and the per-sender charge read it, and they must agree exactly: a volume
// the class calls short but the charge calls satisfied would queue forever without asking for
// anything.
//
// The held count is upToDateVoters and not the member count, because members are joined before
// data is copied into them. The result may be negative.
func computeActualCopiesShort(rv *v1alpha1.ReplicatedVolume) (int, bool) {
	cfg := rv.Status.Configuration
	if cfg == nil {
		// No resolved configuration means no D, so there is no count to fall short of.
		return 0, false
	}

	// Through IntendedDiskfulCount, the single source of the FTT+GMDR+1 formula.
	owed := v1alpha1.IntendedDiskfulCount(cfg.FailuresToTolerate, cfg.GuaranteedMinimumDataRedundancy)

	return owed - int(rv.Status.EffectiveLayout.UpToDateVoters), true
}
