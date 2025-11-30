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

package rvr_status_config_peers_test

import (
	"maps"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types" // cspell:words gomegatypes
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
func HaveAllPeersSet(expectedPeerReplicas []v1alpha3.ReplicatedVolumeReplica) gomegatypes.GomegaMatcher {
	if len(expectedPeerReplicas) < 2 {
		return HaveNoPeers()
	}
	expectedPeers := make(map[string]v1alpha3.Peer, len(expectedPeerReplicas)-1)
	for _, rvr := range expectedPeerReplicas {
		if rvr.Status == nil {
			return gcustom.MakeMatcher(func(_ any) bool { return false }).
				WithMessage("expected rvr to have status, but it's nil")
		}

		if rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
			return gcustom.MakeMatcher(func(_ any) bool { return false }).
				WithMessage("expected rvr to have status.drbd.config, but it's nil")
		}
		diskless := rvr.Status.DRBD.Config.Disk == ""
		expectedPeers[rvr.Spec.NodeName] = v1alpha3.Peer{
			NodeId:   *rvr.Status.DRBD.Config.NodeId,
			Address:  *rvr.Status.DRBD.Config.Address,
			Diskless: diskless,
		}
	}
	return SatisfyAll(
		HaveField("Status.DRBD.Config.Peers", HaveLen(len(expectedPeerReplicas)-1)),
		WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) map[string]v1alpha3.Peer {
			ret := maps.Clone(rvr.Status.DRBD.Config.Peers)
			diskless := rvr.Status.DRBD.Config.Disk == ""
			ret[rvr.Spec.NodeName] = v1alpha3.Peer{
				NodeId:   *rvr.Status.DRBD.Config.NodeId,
				Address:  *rvr.Status.DRBD.Config.Address,
				Diskless: diskless,
			}
			return ret
		}, Equal(expectedPeers)),
	)
}

// makeReady sets up an RVR to be in ready state by initializing Status and DRBD.Config with NodeId and Address
func makeReady(rvr *v1alpha3.ReplicatedVolumeReplica, nodeID uint, address v1alpha3.Address) {
	if rvr.Status == nil {
		rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha3.DRBD{}
	}

	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
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
