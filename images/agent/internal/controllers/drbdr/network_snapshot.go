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

package drbdr

import (
	"context"
	"fmt"
	"net/netip"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// networkSnapshot is read once per reconcile, never from an implicitly created SDN informer.
type networkSnapshot struct {
	addresses map[string]string
	err       error
}

func (r *Reconciler) getNetworkSnapshot(ctx context.Context, resource *v1alpha1.DRBDResource) networkSnapshot {
	snapshot := networkSnapshot{addresses: make(map[string]string)}
	if resource.Spec.State == v1alpha1.DRBDResourceStateDown || isInDeletion(resource) {
		return snapshot
	}
	var required []string
	for _, name := range resource.Spec.SystemNetworks {
		if name != "Internal" {
			required = append(required, name)
		}
	}
	if len(required) == 0 {
		return snapshot
	}
	fail := func(message string) networkSnapshot {
		snapshot.err = ConfiguredReasonError(fmt.Errorf("system networks %v on node %q: %s", required, r.nodeName, message), v1alpha1.DRBDResourceCondConfiguredReasonSystemNetworkAddressUnavailable)
		return snapshot
	}
	if !r.sdnAvailable {
		return fail("SDN disabled or NodeNetworkStatus Kind unavailable until restart")
	}
	nns := &sdn.NodeNetworkStatus{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: r.nodeName}, nns); err != nil {
		return fail(fmt.Sprintf("reading NodeNetworkStatus: %v", err))
	}
	if nns.DeletionTimestamp != nil {
		return fail("NodeNetworkStatus is being deleted")
	}
	for _, address := range nns.Status.Addresses {
		ip, err := netip.ParseAddr(address.Address)
		if address.Type == sdn.NodeNetworkStatusAddressTypeSystemNetworkIP && err == nil && ip.IsGlobalUnicast() {
			snapshot.addresses[address.SystemNetworkName] = address.Address
		}
	}
	for _, name := range required {
		if snapshot.addresses[name] == "" {
			return fail(fmt.Sprintf("SystemNetworkIP address for %q is absent", name))
		}
	}
	return snapshot
}
