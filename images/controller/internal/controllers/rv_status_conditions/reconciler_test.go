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

package rvstatusconditions

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

// TODO: Replace string literal reasons (e.g. "Scheduled", "NoAvailableNodes", "TopologyConstraintsFailed")
// with API constants from v1alpha3 package when other controllers (rvr-scheduling-controller, etc.)
// define them in api/v1alpha3/conditions.go

func setupScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	if err := v1alpha3.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha3 to scheme: %v", err)
	}
	return scheme
}

func newTestReconciler(cl client.Client) *Reconciler {
	return NewReconciler(cl, logr.New(log.NullLogSink{}))
}

// conditionTestCase represents a single test case for condition calculation
type conditionTestCase struct {
	name string

	// RV configuration
	rvName                 string
	replicatedStorageClass string
	replication            string

	// RVRs configuration (list of RVR specs)
	rvrs []testRVR

	// Expected conditions
	wantScheduled            *expectedCondition
	wantBackingVolumeCreated *expectedCondition
	wantConfigured           *expectedCondition
	wantInitialized          *expectedCondition
	wantQuorum               *expectedCondition
	wantDataQuorum           *expectedCondition
	wantIOReady              *expectedCondition

	// Expected counters
	wantDiskfulReplicaCount      string
	wantDiskfulReplicasInSync    string
	wantPublishedAndIOReadyCount string
}

type testRVR struct {
	name     string
	nodeName string
	rvrType  string // "Diskful", "Access", "TieBreaker"

	// Conditions on the RVR
	scheduled            *testCondition
	backingVolumeCreated *testCondition
	configured           *testCondition
	initialSync          *testCondition
	quorum               *testCondition
	devicesReady         *testCondition
}

type testCondition struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

type expectedCondition struct {
	status  metav1.ConditionStatus
	reason  string
	message string // if empty, message is not checked; if set, check that message contains this substring
}

func TestReconciler_RVNotFound(t *testing.T) {
	ctx := context.Background()
	scheme := setupScheme(t)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.ReplicatedVolume{}).
		Build()

	rec := newTestReconciler(cl)

	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "non-existent"},
	})

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got: %+v", result)
	}
}

func TestReconciler_RSCNotFound(t *testing.T) {
	ctx := context.Background()
	scheme := setupScheme(t)

	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-rv",
		},
		Spec: v1alpha3.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "non-existent-rsc",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(rv).
		WithStatusSubresource(&v1alpha3.ReplicatedVolume{}).
		Build()

	rec := newTestReconciler(cl)

	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-rv"},
	})

	// RSC not found is ignored (client.IgnoreNotFound)
	if err != nil {
		t.Errorf("expected no error (RSC not found should be ignored), got: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got: %+v", result)
	}
}

func TestReconciler_ConditionCombinations(t *testing.T) {
	testCases := []conditionTestCase{
		{
			name:                   "all RVRs scheduled and ready",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonBackingVolumeReady},
					configured:           &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonConfigurationAdjustmentSucceeded},
					initialSync:          &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialDeviceReadinessReached},
					quorum:               &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumStatus},
					devicesReady:         &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonDeviceIsReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonBackingVolumeReady},
					configured:           &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonConfigurationAdjustmentSucceeded},
					initialSync:          &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialDeviceReadinessReached},
					quorum:               &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumStatus},
					devicesReady:         &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonDeviceIsReady},
				},
			},
			wantScheduled:             &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonAllReplicasScheduled},
			wantBackingVolumeCreated:  &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonAllBackingVolumesReady},
			wantConfigured:            &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonAllReplicasConfigured},
			wantInitialized:           &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialized},
			wantQuorum:                &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumReached},
			wantDataQuorum:            &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonDataQuorumReached},
			wantIOReady:               &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonRVIOReady},
			wantDiskfulReplicaCount:   "2/2",
			wantDiskfulReplicasInSync: "2/2",
		},
		{
			name:                   "one RVR not scheduled",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonBackingVolumeReady},
					configured:           &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonConfigurationAdjustmentSucceeded},
					initialSync:          &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialDeviceReadinessReached},
					quorum:               &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumStatus},
					devicesReady:         &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonDeviceIsReady},
				},
				{
					name: "rvr-2", nodeName: "", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes match topology constraints"},
				},
			},
			wantScheduled: &expectedCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "rvr-2"},
		},
		{
			name:                   "two RVRs not scheduled with same reason",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes"},
				},
				{
					name: "rvr-2", nodeName: "", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes"},
				},
			},
			wantScheduled: &expectedCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "rvr-1"},
		},
		{
			name:                   "two RVRs not scheduled with different reasons",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes"},
				},
				{
					name: "rvr-2", nodeName: "", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "TopologyConstraintsFailed", message: "topology mismatch"},
				},
			},
			wantScheduled: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonMultipleReasons, message: "rvr-1"},
		},
		{
			name:                     "no RVRs",
			rvName:                   "test-rv",
			replicatedStorageClass:   "test-rsc",
			replication:              v1alpha3.ReplicationAvailability,
			rvrs:                     []testRVR{},
			wantScheduled:            &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonSchedulingInProgress},
			wantBackingVolumeCreated: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonWaitingForBackingVolumes},
			wantConfigured:           &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonConfigurationInProgress},
		},
		{
			name:                   "backing volume not created on one diskful RVR",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonBackingVolumeReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonBackingVolumeCreationFailed, message: "LVM error"},
				},
			},
			wantScheduled:            &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonAllReplicasScheduled},
			wantBackingVolumeCreated: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonBackingVolumeCreationFailed, message: "rvr-2"},
		},
		{
			name:                   "quorum degraded - 2 of 3 in quorum",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					quorum:    &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumStatus},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					quorum:    &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumStatus},
				},
				{
					name: "rvr-3", nodeName: "node-3", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					quorum:    &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonNoQuorumStatus, message: "node offline"},
				},
			},
			wantQuorum: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumDegraded, message: "2/3"},
		},
		{
			name:                   "quorum lost - 1 of 3 in quorum",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					quorum:    &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonQuorumStatus},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					quorum:    &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonNoQuorumStatus},
				},
				{
					name: "rvr-3", nodeName: "node-3", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					quorum:    &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonNoQuorumStatus},
				},
			},
			wantQuorum: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonQuorumLost, message: "1/3"},
		},
		{
			name:                   "initialized with None replication (threshold=1)",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationNone,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:   &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					initialSync: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialDeviceReadinessReached},
				},
			},
			wantInitialized: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialized, message: "1/1"},
		},
		{
			name:                   "not initialized with Availability replication (need 2, have 1)",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:   &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					initialSync: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonInitialDeviceReadinessReached},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:   &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					initialSync: &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonInitialSyncRequiredButNotReady, message: "waiting for sync"},
				},
			},
			wantInitialized: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonInitialSyncRequiredButNotReady, message: "1/2"},
		},
		{
			name:                   "IOReady insufficient - 1 of 2 needed",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:    &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					devicesReady: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonDeviceIsReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:    &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					devicesReady: &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonDeviceIsNotReady, message: "device degraded"},
				},
			},
			wantIOReady: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonDeviceIsNotReady, message: "1/2"},
		},
		{
			name:                   "IOReady none - 0 of 2 needed",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:    &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					devicesReady: &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonDeviceIsNotReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:    &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					devicesReady: &testCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonDeviceIsNotReady},
				},
			},
			wantIOReady: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha3.ReasonNoIOReadyReplicas},
		},
		{
			name:                   "Access replica does not affect backing volume condition",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha3.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha3.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonBackingVolumeReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha3.ReplicaTypeAccess,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					// Access replica has no backing volume
				},
			},
			wantBackingVolumeCreated: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha3.ReasonAllBackingVolumesReady},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runConditionTestCase(t, tc)
		})
	}
}

func runConditionTestCase(t *testing.T, tc conditionTestCase) {
	t.Helper()
	ctx := context.Background()
	scheme := setupScheme(t)

	// Create RV
	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: tc.rvName,
		},
		Spec: v1alpha3.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: tc.replicatedStorageClass,
		},
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{},
			},
		},
	}

	// Create RSC
	rsc := &v1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: tc.replicatedStorageClass,
		},
		Spec: v1alpha1.ReplicatedStorageClassSpec{
			Replication: tc.replication,
		},
	}

	// Create RVRs
	var rvrs []client.Object
	for _, rvrSpec := range tc.rvrs {
		rvr := buildTestRVR(tc.rvName, rvrSpec)
		rvrs = append(rvrs, rvr)
	}

	// Build client
	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(rv, rsc).
		WithStatusSubresource(&v1alpha3.ReplicatedVolume{}, &v1alpha3.ReplicatedVolumeReplica{})

	for _, rvr := range rvrs {
		builder = builder.WithObjects(rvr)
	}

	cl := builder.Build()
	rec := newTestReconciler(cl)

	// Reconcile
	result, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: tc.rvName},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %+v", result)
	}

	// Get updated RV
	updatedRV := &v1alpha3.ReplicatedVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: tc.rvName}, updatedRV); err != nil {
		t.Fatalf("failed to get updated RV: %v", err)
	}

	// Check conditions
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVScheduled, tc.wantScheduled)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVBackingVolumeCreated, tc.wantBackingVolumeCreated)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVConfigured, tc.wantConfigured)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVInitialized, tc.wantInitialized)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVQuorum, tc.wantQuorum)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVDataQuorum, tc.wantDataQuorum)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha3.ConditionTypeRVIOReady, tc.wantIOReady)

	// Check counters
	if tc.wantDiskfulReplicaCount != "" {
		if updatedRV.Status.DiskfulReplicaCount != tc.wantDiskfulReplicaCount {
			t.Errorf("DiskfulReplicaCount: got %q, want %q", updatedRV.Status.DiskfulReplicaCount, tc.wantDiskfulReplicaCount)
		}
	}
	if tc.wantDiskfulReplicasInSync != "" {
		if updatedRV.Status.DiskfulReplicasInSync != tc.wantDiskfulReplicasInSync {
			t.Errorf("DiskfulReplicasInSync: got %q, want %q", updatedRV.Status.DiskfulReplicasInSync, tc.wantDiskfulReplicasInSync)
		}
	}
	if tc.wantPublishedAndIOReadyCount != "" {
		if updatedRV.Status.PublishedAndIOReadyCount != tc.wantPublishedAndIOReadyCount {
			t.Errorf("PublishedAndIOReadyCount: got %q, want %q", updatedRV.Status.PublishedAndIOReadyCount, tc.wantPublishedAndIOReadyCount)
		}
	}
}

func buildTestRVR(rvName string, spec testRVR) *v1alpha3.ReplicatedVolumeReplica {
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: spec.name,
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             spec.nodeName,
			Type:                 spec.rvrType,
		},
		Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{},
		},
	}

	addConditionIfSet(rvr, v1alpha3.ConditionTypeScheduled, spec.scheduled)
	addConditionIfSet(rvr, v1alpha3.ConditionTypeRVRBackingVolumeCreated, spec.backingVolumeCreated)
	addConditionIfSet(rvr, v1alpha3.ConditionTypeConfigurationAdjusted, spec.configured)
	addConditionIfSet(rvr, v1alpha3.ConditionTypeInitialSync, spec.initialSync)
	addConditionIfSet(rvr, v1alpha3.ConditionTypeQuorum, spec.quorum)
	addConditionIfSet(rvr, v1alpha3.ConditionTypeDevicesReady, spec.devicesReady)

	return rvr
}

func addConditionIfSet(rvr *v1alpha3.ReplicatedVolumeReplica, condType string, cond *testCondition) {
	if cond == nil {
		return
	}
	rvr.Status.Conditions = append(rvr.Status.Conditions, metav1.Condition{
		Type:    condType,
		Status:  cond.status,
		Reason:  cond.reason,
		Message: cond.message,
	})
}

func checkCondition(t *testing.T, conditions []metav1.Condition, condType string, want *expectedCondition) {
	t.Helper()
	if want == nil {
		return
	}

	cond := meta.FindStatusCondition(conditions, condType)
	if cond == nil {
		t.Errorf("condition %s not found", condType)
		return
	}

	if cond.Status != want.status {
		t.Errorf("condition %s status: got %v, want %v", condType, cond.Status, want.status)
	}
	if cond.Reason != want.reason {
		t.Errorf("condition %s reason: got %q, want %q", condType, cond.Reason, want.reason)
	}
	if want.message != "" && !strings.Contains(cond.Message, want.message) {
		t.Errorf("condition %s message: got %q, want to contain %q", condType, cond.Message, want.message)
	}
}

// TestReconciler_AggregatedMessages tests the message aggregation logic
func TestReconciler_AggregatedMessages(t *testing.T) {
	testCases := []struct {
		name        string
		failedRVRs  []rvrConditionInfo
		wantReason  string
		wantMessage string
	}{
		{
			name: "single RVR with message",
			failedRVRs: []rvrConditionInfo{
				{name: "rvr-1", reason: "NoAvailableNodes", message: "no nodes match topology"},
			},
			wantReason:  "NoAvailableNodes",
			wantMessage: "rvr-1 - NoAvailableNodes - no nodes match topology",
		},
		{
			name: "single RVR without message",
			failedRVRs: []rvrConditionInfo{
				{name: "rvr-1", reason: "NoAvailableNodes"},
			},
			wantReason:  "NoAvailableNodes",
			wantMessage: "rvr-1 - NoAvailableNodes",
		},
		{
			name: "multiple RVRs same reason with messages",
			failedRVRs: []rvrConditionInfo{
				{name: "rvr-1", reason: "NoAvailableNodes", message: "no nodes"},
				{name: "rvr-2", reason: "NoAvailableNodes", message: "no nodes"},
			},
			wantReason:  "NoAvailableNodes",
			wantMessage: "rvr-1 - NoAvailableNodes - no nodes, rvr-2 - NoAvailableNodes - no nodes",
		},
		{
			name: "multiple RVRs different reasons",
			failedRVRs: []rvrConditionInfo{
				{name: "rvr-1", reason: "NoAvailableNodes", message: "no nodes"},
				{name: "rvr-2", reason: "TopologyConstraintsFailed", message: "topology mismatch"},
			},
			wantReason:  v1alpha3.ReasonMultipleReasons,
			wantMessage: "rvr-1 - NoAvailableNodes - no nodes, rvr-2 - TopologyConstraintsFailed - topology mismatch",
		},
		{
			name: "three RVRs two same reasons one different",
			failedRVRs: []rvrConditionInfo{
				{name: "rvr-1", reason: "NoAvailableNodes", message: "msg1"},
				{name: "rvr-2", reason: "NoAvailableNodes", message: "msg2"},
				{name: "rvr-3", reason: "TopologyConstraintsFailed", message: "msg3"},
			},
			wantReason:  v1alpha3.ReasonMultipleReasons,
			wantMessage: "rvr-1 - NoAvailableNodes - msg1, rvr-2 - NoAvailableNodes - msg2, rvr-3 - TopologyConstraintsFailed - msg3",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reason, message := aggregateCondition(tc.failedRVRs)
			if reason != tc.wantReason {
				t.Errorf("reason: got %q, want %q", reason, tc.wantReason)
			}
			if message != tc.wantMessage {
				t.Errorf("message: got %q, want %q", message, tc.wantMessage)
			}
		})
	}
}
