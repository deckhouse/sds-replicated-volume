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

package datamesh

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Testing approach
//
// *_plan_*_test.go files test plan BEHAVIOR through ProcessTransitions:
// guards, apply, confirm, onComplete, plan selection.
//
// They do NOT re-test engine mechanics (dmte/*_test.go), concurrency rules
// (concurrency_tracker_test.go), context building/writebacks (context_test.go),
// or slot accessors (slots_test.go).
//
// A test belongs in a plan test file if it catches a regression when someone
// changes a plan or dispatch file (*_plan_*.go, *_dispatch.go). If it only
// catches engine or infrastructure regressions, it belongs in the corresponding
// package's tests.

func TestDatamesh(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "datamesh Suite")
}

var _ = BeforeSuite(func() {
	BuildRegistry()
})

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
//

// minimalConfig is a non-nil Configuration for buildContexts / ProcessTransitions tests.
var minimalConfig = &v1alpha1.ReplicatedVolumeConfiguration{
	ReplicatedStoragePoolName: "test-pool",
	Topology:                  v1alpha1.TopologyIgnored,
	VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
}

// testRSP is a simple RSP implementation for tests.
// Uses linear scan (no sort requirement).
type testRSP struct {
	nodes []v1alpha1.ReplicatedStoragePoolEligibleNode
}

func (r *testRSP) FindEligibleNode(nodeName string) *v1alpha1.ReplicatedStoragePoolEligibleNode {
	for i := range r.nodes {
		if r.nodes[i].NodeName == nodeName {
			return &r.nodes[i]
		}
	}
	return nil
}

// mkRSP creates a testRSP with one EligibleNode per node name.
func mkRSP(nodeNames ...string) *testRSP {
	nodes := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, len(nodeNames))
	for i, n := range nodeNames {
		nodes[i] = v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: n, NodeReady: true, AgentReady: true}
	}
	return &testRSP{nodes: nodes}
}

// mkRSPWithZones creates a testRSP with nodes and zones.
func mkRSPWithZones(pairs ...string) *testRSP {
	nodes := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		nodes = append(nodes, v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: pairs[i], ZoneName: pairs[i+1],
			NodeReady: true, AgentReady: true,
		})
	}
	return &testRSP{nodes: nodes}
}

// mkRV creates a ReplicatedVolume with Configuration and the given status fields.
func mkRV(
	revision int64,
	members []v1alpha1.DatameshMember,
	requests []v1alpha1.ReplicatedVolumeDatameshReplicaRequest,
	transitions []v1alpha1.ReplicatedVolumeDatameshTransition,
) *v1alpha1.ReplicatedVolume {
	cfg := *minimalConfig // copy so tests can safely mutate rv.Status.Configuration
	return &v1alpha1.ReplicatedVolume{
		Spec: v1alpha1.ReplicatedVolumeSpec{MaxAttachments: 1},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration:           &cfg,
			DatameshRevision:        revision,
			Datamesh:                v1alpha1.ReplicatedVolumeDatamesh{Members: members},
			DatameshReplicaRequests: requests,
			DatameshTransitions:     transitions,
		},
	}
}

// mkMember creates a DatameshMember with name, type, and nodeName.
func mkMember(name string, memberType v1alpha1.DatameshMemberType, nodeName string) v1alpha1.DatameshMember {
	m := v1alpha1.DatameshMember{
		Name:     name,
		Type:     memberType,
		NodeName: nodeName,
	}
	if memberType.HasBackingVolume() ||
		memberType == v1alpha1.DatameshMemberTypeLiminalDiskful ||
		memberType == v1alpha1.DatameshMemberTypeLiminalShadowDiskful {
		m.LVMVolumeGroupName = "test-lvg"
		m.LVMVolumeGroupThinPoolName = "test-thin"
	}
	return m
}

// mkRVR creates a ReplicatedVolumeReplica with a default address.
func mkRVR(name, nodeName string, datameshRevision int64) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: nodeName},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			DatameshRevision: datameshRevision,
			Addresses:        []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
		},
	}
}

// mkRVRUpToDate creates a ReplicatedVolumeReplica with BackingVolumeUpToDate=True.
// Used for D removal tests where guards check UpToDate D count.
func mkRVRUpToDate(name, nodeName string, datameshRevision int64) *v1alpha1.ReplicatedVolumeReplica {
	rvr := mkRVR(name, nodeName, datameshRevision)
	rvr.Status.Conditions = []metav1.Condition{
		{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType,
			Status: metav1.ConditionTrue,
		},
	}
	return rvr
}

// mkRVRBare creates a ReplicatedVolumeReplica without addresses (for guard tests).
func mkRVRBare(name, nodeName string) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: nodeName},
	}
}

// mkJoinRequestAccess creates a Join request for Access type.
func mkJoinRequestAccess(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest {
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      v1alpha1.ReplicaTypeAccess,
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkJoinRequestTB creates a Join request for TieBreaker type.
func mkJoinRequestTB(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      v1alpha1.ReplicaTypeTieBreaker,
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkJoinRequestSD creates a Join request for ShadowDiskful type with BV fields.
func mkJoinRequestSD(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation:          v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:               v1alpha1.ReplicaTypeShadowDiskful,
			LVMVolumeGroupName: "test-lvg",
			ThinPoolName:       "test-thin",
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkJoinRequestD creates a Join request for Diskful type with BV fields.
func mkJoinRequestD(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation:          v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:               v1alpha1.ReplicaTypeDiskful,
			LVMVolumeGroupName: "test-lvg",
			ThinPoolName:       "test-thin",
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkChangeRoleRequest creates a ChangeRole request with the given target type.
// LVG/ThinPool are always set; they are ignored for non-BV transitions.
func mkChangeRoleRequest(name string, targetType v1alpha1.ReplicaType) v1alpha1.ReplicatedVolumeDatameshReplicaRequest {
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation:          v1alpha1.DatameshMembershipRequestOperationChangeRole,
			Type:               targetType,
			LVMVolumeGroupName: "test-lvg",
			ThinPoolName:       "test-thin",
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkForceDetachRequest creates a ForceDetach request.
func mkForceDetachRequest(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationForceDetach,
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkForceLeaveRequest creates a ForceLeave request.
func mkForceLeaveRequest(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest {
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationForceLeave,
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkLeaveRequest creates a Leave request.
func mkLeaveRequest(name string) v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
	return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		Name: name,
		Request: v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationLeave,
		},
		FirstObservedAt: metav1.Now(),
	}
}

// mkRVRReady creates a ReplicatedVolumeReplica with Ready condition and Quorum=true.
// Used by attachment tests where guardRVRReady and guardQuorumSatisfied must pass.
func mkRVRReady(name, nodeName string, datameshRevision int64) *v1alpha1.ReplicatedVolumeReplica {
	rvr := mkRVR(name, nodeName, datameshRevision)
	rvr.Generation = 1
	rvr.Status.Quorum = ptr.To(true)
	rvr.Status.Conditions = []metav1.Condition{{
		Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:             metav1.ConditionTrue,
		Reason:             "Ready",
		ObservedGeneration: 1,
	}}
	return rvr
}

// mkRVA creates a ReplicatedVolumeAttachment for a given node.
func mkRVA(name, nodeName string) *v1alpha1.ReplicatedVolumeAttachment {
	return &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.Now()},
		Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: nodeName},
	}
}

// mkMemberAttached creates a DatameshMember with Attached=true.
func mkMemberAttached(name string, memberType v1alpha1.DatameshMemberType, nodeName string) v1alpha1.DatameshMember {
	m := mkMember(name, memberType, nodeName)
	m.Attached = true
	return m
}
