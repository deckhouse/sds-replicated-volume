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

package rvrstatusconditions

import (
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

// conditionTestCase defines a test case for reconciler condition logic
type conditionTestCase struct {
	name string

	// Input RVR conditions (nil = condition missing)
	scheduled   *bool
	initialized *bool
	inQuorum    *bool
	inSync      *bool

	// Input RVR conditions with custom reasons (optional)
	scheduledReason   string
	initializedReason string
	inQuorumReason    string
	inSyncReason      string

	// RVR state
	hasDeletionTimestamp bool // RVR is being deleted but has finalizers

	// Agent/Node state
	agentReady bool
	nodeReady  bool
	nodeExists bool
	nodeName   string // defaults to "test-node"

	// Expected output
	wantOnlineStatus  metav1.ConditionStatus
	wantOnlineReason  string
	wantIOReadyStatus metav1.ConditionStatus
	wantIOReadyReason string
}

func TestReconciler_ConditionCombinations(t *testing.T) {
	tests := []conditionTestCase{
		// === Happy path ===
		{
			name:              "all conditions true, agent ready → Online=True, IOReady=True",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionTrue,
			wantOnlineReason:  v1alpha3.ReasonOnline,
			wantIOReadyStatus: metav1.ConditionTrue,
			wantIOReadyReason: v1alpha3.ReasonIOReady,
		},

		// === Scheduled=False ===
		{
			name:              "Scheduled=False → Online=False (copies reason), IOReady=False (Offline)",
			scheduled:         u.Ptr(false),
			scheduledReason:   "WaitingForNode",
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  "WaitingForNode", // copied from source
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},

		// === Initialized=False ===
		{
			name:              "Initialized=False → Online=False (copies reason), IOReady=False (Offline)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(false),
			initializedReason: "WaitingForSync",
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  "WaitingForSync", // copied from source
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},

		// === InQuorum=False ===
		{
			name:              "InQuorum=False → Online=False (copies reason), IOReady=False (Offline)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(false),
			inQuorumReason:    "NoQuorum",
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  "NoQuorum", // copied from source
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},

		// === InSync=False (Online but not IOReady) ===
		{
			name:              "InSync=False → Online=True, IOReady=False (copies reason)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(false),
			inSyncReason:      "Synchronizing",
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionTrue,
			wantOnlineReason:  v1alpha3.ReasonOnline,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: "Synchronizing", // copied from source
		},

		// === Agent/Node not ready ===
		{
			name:              "Agent not ready, Node ready → Online=False (AgentNotReady), IOReady=False (AgentNotReady)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        false,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  v1alpha3.ReasonAgentNotReady,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonAgentNotReady,
		},
		{
			name:              "Node not ready → Online=False (NodeNotReady), IOReady=False (NodeNotReady)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        false,
			nodeReady:         false,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  v1alpha3.ReasonNodeNotReady,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonNodeNotReady,
		},
		{
			name:              "Node does not exist → Online=False (NodeNotReady), IOReady=False (NodeNotReady)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        false,
			nodeReady:         false,
			nodeExists:        false,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  v1alpha3.ReasonNodeNotReady,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonNodeNotReady,
		},

		// === Missing conditions (nil) ===
		{
			name:              "Scheduled missing → Online=False (Unscheduled), IOReady=False (Offline)",
			scheduled:         nil, // missing
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  v1alpha3.ReasonUnscheduled,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},
		{
			name:              "Initialized missing → Online=False (Uninitialized), IOReady=False (Offline)",
			scheduled:         u.Ptr(true),
			initialized:       nil, // missing
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  v1alpha3.ReasonUninitialized,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},
		{
			name:              "InQuorum missing → Online=False (QuorumLost), IOReady=False (Offline)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          nil, // missing
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  v1alpha3.ReasonQuorumLost,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},
		{
			name:              "InSync missing → Online=True, IOReady=False (OutOfSync)",
			scheduled:         u.Ptr(true),
			initialized:       u.Ptr(true),
			inQuorum:          u.Ptr(true),
			inSync:            nil, // missing
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionTrue,
			wantOnlineReason:  v1alpha3.ReasonOnline,
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOutOfSync,
		},

		// === Multiple conditions false (priority check) ===
		{
			name:              "Scheduled=False AND Initialized=False → copies Scheduled reason (checked first)",
			scheduled:         u.Ptr(false),
			scheduledReason:   "NotScheduled",
			initialized:       u.Ptr(false),
			initializedReason: "NotInitialized",
			inQuorum:          u.Ptr(true),
			inSync:            u.Ptr(true),
			agentReady:        true,
			nodeReady:         true,
			nodeExists:        true,
			wantOnlineStatus:  metav1.ConditionFalse,
			wantOnlineReason:  "NotScheduled", // Scheduled checked first
			wantIOReadyStatus: metav1.ConditionFalse,
			wantIOReadyReason: v1alpha3.ReasonOffline,
		},

		// === DeletionTimestamp (still updates conditions for finalizer controllers) ===
		{
			name:                 "RVR with DeletionTimestamp still updates conditions",
			scheduled:            u.Ptr(true),
			initialized:          u.Ptr(true),
			inQuorum:             u.Ptr(true),
			inSync:               u.Ptr(true),
			hasDeletionTimestamp: true,
			agentReady:           true,
			nodeReady:            true,
			nodeExists:           true,
			wantOnlineStatus:     metav1.ConditionTrue,
			wantOnlineReason:     v1alpha3.ReasonOnline,
			wantIOReadyStatus:    metav1.ConditionTrue,
			wantIOReadyReason:    v1alpha3.ReasonIOReady,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runConditionTestCase(t, tc)
		})
	}
}

func runConditionTestCase(t *testing.T, tc conditionTestCase) {
	t.Helper()

	ctx := t.Context()
	nodeName := tc.nodeName
	if nodeName == "" {
		nodeName = "test-node"
	}

	// Setup scheme with required types
	s := scheme.Scheme
	if err := v1alpha3.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha3 to scheme: %v", err)
	}

	// Build RVR
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-rvr",
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			NodeName: nodeName,
		},
		Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
			Conditions: buildConditions(tc),
		},
	}

	// Add DeletionTimestamp if needed (RVR is being deleted but has finalizers)
	if tc.hasDeletionTimestamp {
		now := metav1.Now()
		rvr.DeletionTimestamp = &now
		rvr.Finalizers = []string{"test-finalizer"}
	}

	// Build objects for fake client
	objects := []client.Object{rvr}

	// Add Node if exists
	if tc.nodeExists {
		nodeReadyStatus := corev1.ConditionFalse
		if tc.nodeReady {
			nodeReadyStatus = corev1.ConditionTrue
		}
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: nodeReadyStatus},
				},
			},
		}
		objects = append(objects, node)
	}

	// Add Agent pod if ready
	if tc.agentReady {
		agentPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-" + nodeName,
				Namespace: v1alpha3.ModuleNamespace,
				Labels:    map[string]string{AgentPodLabel: AgentPodValue},
			},
			Spec: corev1.PodSpec{NodeName: nodeName},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		}
		objects = append(objects, agentPod)
	}

	// Build fake client
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
		Build()

	// Create reconciler
	rec := NewReconciler(cl, logr.Discard())

	// Run reconcile
	_, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-rvr"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Get updated RVR
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	// Assert Online condition
	onlineCond := meta.FindStatusCondition(updatedRVR.Status.Conditions, v1alpha3.ConditionTypeOnline)
	if onlineCond == nil {
		t.Error("Online condition not found")
	} else {
		if onlineCond.Status != tc.wantOnlineStatus {
			t.Errorf("Online.Status: got %v, want %v", onlineCond.Status, tc.wantOnlineStatus)
		}
		if onlineCond.Reason != tc.wantOnlineReason {
			t.Errorf("Online.Reason: got %q, want %q", onlineCond.Reason, tc.wantOnlineReason)
		}
	}

	// Assert IOReady condition
	ioReadyCond := meta.FindStatusCondition(updatedRVR.Status.Conditions, v1alpha3.ConditionTypeIOReady)
	if ioReadyCond == nil {
		t.Error("IOReady condition not found")
	} else {
		if ioReadyCond.Status != tc.wantIOReadyStatus {
			t.Errorf("IOReady.Status: got %v, want %v", ioReadyCond.Status, tc.wantIOReadyStatus)
		}
		if ioReadyCond.Reason != tc.wantIOReadyReason {
			t.Errorf("IOReady.Reason: got %q, want %q", ioReadyCond.Reason, tc.wantIOReadyReason)
		}
	}
}

func buildConditions(tc conditionTestCase) []metav1.Condition {
	var conditions []metav1.Condition

	if tc.scheduled != nil {
		status := metav1.ConditionFalse
		if *tc.scheduled {
			status = metav1.ConditionTrue
		}
		reason := tc.scheduledReason
		if reason == "" {
			reason = "Scheduled"
		}
		conditions = append(conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeScheduled,
			Status: status,
			Reason: reason,
		})
	}

	if tc.initialized != nil {
		status := metav1.ConditionFalse
		if *tc.initialized {
			status = metav1.ConditionTrue
		}
		reason := tc.initializedReason
		if reason == "" {
			reason = "Initialized"
		}
		conditions = append(conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeDataInitialized,
			Status: status,
			Reason: reason,
		})
	}

	if tc.inQuorum != nil {
		status := metav1.ConditionFalse
		if *tc.inQuorum {
			status = metav1.ConditionTrue
		}
		reason := tc.inQuorumReason
		if reason == "" {
			reason = "InQuorum"
		}
		conditions = append(conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeInQuorum,
			Status: status,
			Reason: reason,
		})
	}

	if tc.inSync != nil {
		status := metav1.ConditionFalse
		if *tc.inSync {
			status = metav1.ConditionTrue
		}
		reason := tc.inSyncReason
		if reason == "" {
			reason = "InSync"
		}
		conditions = append(conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeInSync,
			Status: status,
			Reason: reason,
		})
	}

	return conditions
}

// === Edge case test: RVR not found ===

func TestReconciler_RVRNotFound(t *testing.T) {
	ctx := t.Context()

	// Setup scheme with required types
	s := scheme.Scheme
	if err := v1alpha3.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha3 to scheme: %v", err)
	}

	// Build fake client with no RVR
	cl := fake.NewClientBuilder().
		WithScheme(s).
		Build()

	// Create reconciler
	rec := NewReconciler(cl, logr.Discard())

	// Run reconcile for non-existent RVR
	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "non-existent-rvr"},
	})

	// Should return no error and no requeue
	if err != nil {
		t.Errorf("expected no error for NotFound, got: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got: %+v", result)
	}
}
