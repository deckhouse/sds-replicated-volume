/*
Copyright 2025 Flant JSC

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

package rvrstatusconfigpeers_test

import (
	"context"
	"maps"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types" // cspell:words gomegatypes
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestRvrStatusConfigPeers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrStatusConfigPeers Suite")
}

// HaveNoPeers is a Gomega matcher that checks a single RVR has no peers
func HaveNoPeers() gomegatypes.GomegaMatcher {
	return SatisfyAny(
		HaveField("Status", BeNil()),
		HaveField("Status.DRBD", BeNil()),
		HaveField("Status.DRBD.Config", BeNil()),
		HaveField("Status.DRBD.Config.Peers", BeEmpty()),
	)
}

// HaveAllPeersSet is a matcher factory that returns a Gomega matcher for a single RVR
// It checks that the RVR has all other RVRs from expectedResources as peers but his own
func HaveAllPeersSet(expectedPeerReplicas []v1alpha1.ReplicatedVolumeReplica) gomegatypes.GomegaMatcher {
	if len(expectedPeerReplicas) < 2 {
		return HaveNoPeers()
	}
	expectedPeers := make(map[string]v1alpha1.Peer, len(expectedPeerReplicas)-1)
	for _, rvr := range expectedPeerReplicas {
		if rvr.Status == nil {
			return gcustom.MakeMatcher(func(_ any) bool { return false }).
				WithMessage("expected rvr to have status, but it's nil")
		}

		if rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
			return gcustom.MakeMatcher(func(_ any) bool { return false }).
				WithMessage("expected rvr to have status.drbd.config, but it's nil")
		}
		expectedPeers[rvr.Spec.NodeName] = v1alpha1.Peer{
			NodeId:   *rvr.Status.DRBD.Config.NodeId,
			Address:  *rvr.Status.DRBD.Config.Address,
			Diskless: rvr.Spec.IsDiskless(),
		}
	}
	return SatisfyAll(
		HaveField("Status.DRBD.Config.Peers", HaveLen(len(expectedPeerReplicas)-1)),
		WithTransform(func(rvr v1alpha1.ReplicatedVolumeReplica) map[string]v1alpha1.Peer {
			ret := maps.Clone(rvr.Status.DRBD.Config.Peers)
			ret[rvr.Spec.NodeName] = v1alpha1.Peer{
				NodeId:   *rvr.Status.DRBD.Config.NodeId,
				Address:  *rvr.Status.DRBD.Config.Address,
				Diskless: rvr.Spec.IsDiskless(),
			}
			return ret
		}, Equal(expectedPeers)),
	)
}

// makeReady sets up an RVR to be in ready state by initializing Status and DRBD.Config with NodeId and Address
func makeReady(rvr *v1alpha1.ReplicatedVolumeReplica, nodeID uint, address v1alpha1.Address) {
	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}

	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
	}

	rvr.Status.DRBD.Config.NodeId = &nodeID
	rvr.Status.DRBD.Config.Address = &address
}

// BeReady returns a matcher that checks if an RVR is in ready state (has NodeName, NodeId, and Address)
func BeReady() gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Spec.NodeName", Not(BeEmpty())),
		HaveField("Status.DRBD.Config.NodeId", Not(BeNil())),
		HaveField("Status.DRBD.Config.Address", Not(BeNil())),
	)
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// InterceptGet creates an interceptor that modifies objects in both Get and List operations.
// If Get or List returns an error, intercept is called with a nil (zero) value of type T allowing alternating the error.
func InterceptGet[T client.Object](
	intercept func(T) error,
) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			targetObj, ok := obj.(T)
			if !ok {
				return cl.Get(ctx, key, obj, opts...)
			}
			if err := cl.Get(ctx, key, obj, opts...); err != nil {
				var zero T
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			if err := intercept(targetObj); err != nil {
				return err
			}
			return nil
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			v := reflect.ValueOf(list).Elem()
			itemsField := v.FieldByName("Items")
			if !itemsField.IsValid() || itemsField.Kind() != reflect.Slice {
				return cl.List(ctx, list, opts...)
			}
			if err := cl.List(ctx, list, opts...); err != nil {
				var zero T
				// Check if any items in the list would be of type T
				// We can't know for sure without the list, but we can try to intercept with nil
				// This allows intercept to handle the error case
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			// Intercept items after List populates them
			for i := 0; i < itemsField.Len(); i++ {
				item := itemsField.Index(i).Addr().Interface().(client.Object)
				if targetObj, ok := item.(T); ok {
					if err := intercept(targetObj); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
}
