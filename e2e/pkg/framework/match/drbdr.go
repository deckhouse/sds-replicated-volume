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
