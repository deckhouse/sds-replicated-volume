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
	"sort"
	"strings"

	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// DRBDR is the namespace for DRBDResource-specific matchers.
var DRBDR drbdr

type drbdr struct{}

func asDRBDR(obj client.Object) *v1alpha1.DRBDResource {
	d, ok := obj.(*v1alpha1.DRBDResource)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.DRBDResource, got %T", obj))
	}
	return d
}

// DiskState matches when status.diskState equals the expected value.
func (drbdr) DiskState(expected v1alpha1.DiskState) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if d.Status.DiskState == expected {
			return true, fmt.Sprintf("diskState is %s", d.Status.DiskState)
		}
		return false, fmt.Sprintf("diskState is %s, expected %s", d.Status.DiskState, expected)
	})
}

// HasAddresses matches when status.addresses is non-empty.
func (drbdr) HasAddresses() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if len(d.Status.Addresses) > 0 {
			return true, fmt.Sprintf("has %d address(es)", len(d.Status.Addresses))
		}
		return false, "no addresses"
	})
}

// HasDevice matches when status.device is non-empty.
func (drbdr) HasDevice() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if d.Status.Device != "" {
			return true, fmt.Sprintf("device is %s", d.Status.Device)
		}
		return false, "no device"
	})
}

// IOSuspended matches when status.deviceIOSuspended is true.
func (drbdr) IOSuspended() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if d.Status.DeviceIOSuspended != nil && *d.Status.DeviceIOSuspended {
			return true, "device I/O is suspended"
		}
		return false, "device I/O is not suspended"
	})
}

// PeersMatchSpec matches when peer names from spec.Peers match peer names from status.Peers.
func (drbdr) PeersMatchSpec() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		specNames := peerNames(d.Spec.Peers)
		statusNames := statusPeerNames(d.Status.Peers)
		if specNames == statusNames {
			if specNames == "" {
				return true, "peers match spec (no peers)"
			}
			return true, fmt.Sprintf("peers match spec: [%s]", specNames)
		}
		return false, fmt.Sprintf("peers mismatch: spec=[%s], status=[%s]", specNames, statusNames)
	})
}

// RoleMatchesSpec matches when spec.Role equals status.ActiveConfiguration.Role.
func (drbdr) RoleMatchesSpec() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if d.Status.ActiveConfiguration == nil {
			return false, "activeConfiguration is nil"
		}
		if d.Spec.Role == d.Status.ActiveConfiguration.Role {
			return true, fmt.Sprintf("role matches spec: %s", d.Spec.Role)
		}
		return false, fmt.Sprintf("role mismatch: spec=%s, status=%s", d.Spec.Role, d.Status.ActiveConfiguration.Role)
	})
}

// LVMMatchesSpec matches when spec.LVMLogicalVolumeName equals status.ActiveConfiguration.LVMLogicalVolumeName.
func (drbdr) LVMMatchesSpec() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if d.Status.ActiveConfiguration == nil {
			return false, "activeConfiguration is nil"
		}
		if d.Spec.LVMLogicalVolumeName == d.Status.ActiveConfiguration.LVMLogicalVolumeName {
			return true, fmt.Sprintf("LVM matches spec: %q", d.Spec.LVMLogicalVolumeName)
		}
		return false, fmt.Sprintf("LVM mismatch: spec=%q, status=%q",
			d.Spec.LVMLogicalVolumeName, d.Status.ActiveConfiguration.LVMLogicalVolumeName)
	})
}

// QuorumMatchesSpec matches when spec.Quorum equals status.ActiveConfiguration.Quorum.
func (drbdr) QuorumMatchesSpec() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d := asDRBDR(obj)
		if d.Status.ActiveConfiguration == nil {
			return false, "activeConfiguration is nil"
		}
		if d.Status.ActiveConfiguration.Quorum == nil {
			return false, "activeConfiguration.quorum is nil"
		}
		if d.Spec.Quorum == *d.Status.ActiveConfiguration.Quorum {
			return true, fmt.Sprintf("quorum matches spec: %d", d.Spec.Quorum)
		}
		return false, fmt.Sprintf("quorum mismatch: spec=%d, status=%d",
			d.Spec.Quorum, *d.Status.ActiveConfiguration.Quorum)
	})
}

func peerNames(peers []v1alpha1.DRBDResourcePeer) string {
	names := make([]string, len(peers))
	for i := range peers {
		names[i] = peers[i].Name
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func statusPeerNames(peers []v1alpha1.DRBDResourcePeerStatus) string {
	names := make([]string, len(peers))
	for i := range peers {
		names[i] = peers[i].Name
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
