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
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

func setupScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := scheme.Scheme
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	return s
}

func newTestReconciler(cl client.Client) *Reconciler {
	return NewReconciler(cl, logr.Discard())
}

func withRVRIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolumeReplica{}, indexes.IndexFieldRVRByReplicatedVolumeName, func(obj client.Object) []string {
		rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
		if !ok {
			return nil
		}
		if rvr.Spec.ReplicatedVolumeName == "" {
			return nil
		}
		return []string{rvr.Spec.ReplicatedVolumeName}
	})
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
	wantDiskfulReplicaCount     string
	wantDiskfulReplicasInSync   string
	wantAttachedAndIOReadyCount string
}

type testRVR struct {
	name     string
	nodeName string
	rvrType  v1alpha1.ReplicaType

	// Conditions on the RVR (using spec-compliant names)
	scheduled            *testCondition
	backingVolumeCreated *testCondition
	configured           *testCondition
	dataInitialized      *testCondition // DataInitialized - set by drbd-config-controller (agent)
	inQuorum             *testCondition // InQuorum per spec
	inSync               *testCondition // InSync per spec
	ioReady              *testCondition // IOReady per spec (computed by rvr-status-conditions)
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
	ctx := t.Context()
	s := setupScheme(t)

	cl := withRVRIndex(fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&v1alpha1.ReplicatedVolume{})).
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
	ctx := t.Context()
	s := setupScheme(t)

	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-rv",
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "non-existent-rsc",
		},
	}

	cl := withRVRIndex(fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(rv).
		WithStatusSubresource(&v1alpha1.ReplicatedVolume{})).
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
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonBackingVolumeReady},
					configured:           &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonConfigurationAdjustmentSucceeded},
					dataInitialized:      &testCondition{status: metav1.ConditionTrue, reason: "Initialized"},
					inQuorum:             &testCondition{status: metav1.ConditionTrue, reason: "InQuorum"},
					inSync:               &testCondition{status: metav1.ConditionTrue, reason: "InSync"},
					ioReady:              &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonIOReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonBackingVolumeReady},
					configured:           &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonConfigurationAdjustmentSucceeded},
					dataInitialized:      &testCondition{status: metav1.ConditionTrue, reason: "Initialized"},
					inQuorum:             &testCondition{status: metav1.ConditionTrue, reason: "InQuorum"},
					inSync:               &testCondition{status: metav1.ConditionTrue, reason: "InSync"},
					ioReady:              &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonIOReady},
				},
			},
			wantScheduled:             &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonAllReplicasScheduled},
			wantBackingVolumeCreated:  &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonAllBackingVolumesReady},
			wantConfigured:            &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonAllReplicasConfigured},
			wantInitialized:           &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonInitialized},
			wantQuorum:                &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonQuorumReached},
			wantDataQuorum:            &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonDataQuorumReached},
			wantIOReady:               &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonRVIOReady},
			wantDiskfulReplicaCount:   "2/2",
			wantDiskfulReplicasInSync: "2/2",
		},
		{
			name:                   "one RVR not scheduled",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonBackingVolumeReady},
					configured:           &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonConfigurationAdjustmentSucceeded},
					dataInitialized:      &testCondition{status: metav1.ConditionTrue, reason: "Initialized"},
					inQuorum:             &testCondition{status: metav1.ConditionTrue, reason: "InQuorum"},
					inSync:               &testCondition{status: metav1.ConditionTrue, reason: "InSync"},
					ioReady:              &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonIOReady},
				},
				{
					name: "rvr-2", nodeName: "", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes match topology constraints"},
				},
			},
			// Now we use RV-level reasons, not RVR reasons
			wantScheduled: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonReplicasNotScheduled, message: "1/2"},
		},
		{
			name:                   "two RVRs not scheduled",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes"},
				},
				{
					name: "rvr-2", nodeName: "", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionFalse, reason: "NoAvailableNodes", message: "no nodes"},
				},
			},
			// Simple RV-level reason, not aggregated RVR reasons
			wantScheduled: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonReplicasNotScheduled, message: "0/2"},
		},
		{
			name:                     "no RVRs",
			rvName:                   "test-rv",
			replicatedStorageClass:   "test-rsc",
			replication:              v1alpha1.ReplicationAvailability,
			rvrs:                     []testRVR{},
			wantScheduled:            &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonSchedulingInProgress},
			wantBackingVolumeCreated: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonWaitingForBackingVolumes},
			wantConfigured:           &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonConfigurationInProgress},
			wantInitialized:          &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonWaitingForReplicas},
		},
		{
			name:                   "backing volume not created on one diskful RVR",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonBackingVolumeReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonBackingVolumeCreationFailed, message: "LVM error"},
				},
			},
			wantScheduled: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonAllReplicasScheduled},
			// Now we use RV-level reason
			wantBackingVolumeCreated: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonBackingVolumesNotReady, message: "1/2"},
		},
		{
			name:                   "quorum degraded - 2 of 3 in quorum",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					inQuorum:  &testCondition{status: metav1.ConditionTrue, reason: "InQuorum"},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					inQuorum:  &testCondition{status: metav1.ConditionTrue, reason: "InQuorum"},
				},
				{
					name: "rvr-3", nodeName: "node-3", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					inQuorum:  &testCondition{status: metav1.ConditionFalse, reason: "QuorumLost", message: "node offline"},
				},
			},
			wantQuorum: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonQuorumDegraded, message: "2/3"},
		},
		{
			name:                   "quorum lost - 1 of 3 in quorum",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationConsistencyAndAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					inQuorum:  &testCondition{status: metav1.ConditionTrue, reason: "InQuorum"},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					inQuorum:  &testCondition{status: metav1.ConditionFalse, reason: "QuorumLost"},
				},
				{
					name: "rvr-3", nodeName: "node-3", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					inQuorum:  &testCondition{status: metav1.ConditionFalse, reason: "QuorumLost"},
				},
			},
			wantQuorum: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonQuorumLost, message: "1/3"},
		},
		{
			name:                   "initialized with None replication (threshold=1)",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationNone,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:       &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					dataInitialized: &testCondition{status: metav1.ConditionTrue, reason: "Initialized"},
				},
			},
			wantInitialized: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonInitialized, message: "1/1"},
		},
		{
			name:                   "not initialized with Availability replication (need 2, have 1)",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:       &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					dataInitialized: &testCondition{status: metav1.ConditionTrue, reason: "Initialized"},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:       &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					dataInitialized: &testCondition{status: metav1.ConditionFalse, reason: "WaitingForInitialSync", message: "waiting for sync"},
				},
			},
			// Now we use RV-level reason
			wantInitialized: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonInitializationInProgress, message: "1/2"},
		},
		{
			name:                   "IOReady insufficient - 1 of 2 needed",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					ioReady:   &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonIOReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					ioReady:   &testCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonOffline, message: "device degraded"},
				},
			},
			// Now we use RV-level reason
			wantIOReady: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonInsufficientIOReadyReplicas, message: "1/2"},
		},
		{
			name:                   "IOReady none - 0 of 2 needed",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					ioReady:   &testCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonOffline},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					ioReady:   &testCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonOffline},
				},
			},
			wantIOReady: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonNoIOReadyReplicas},
		},
		{
			name:                   "Access replica does not affect backing volume condition",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:            &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					backingVolumeCreated: &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonBackingVolumeReady},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeAccess,
					scheduled: &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					// Access replica has no backing volume
				},
			},
			wantBackingVolumeCreated: &expectedCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonAllBackingVolumesReady},
		},
		{
			name:                   "configured - some not configured",
			rvName:                 "test-rv",
			replicatedStorageClass: "test-rsc",
			replication:            v1alpha1.ReplicationAvailability,
			rvrs: []testRVR{
				{
					name: "rvr-1", nodeName: "node-1", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:  &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					configured: &testCondition{status: metav1.ConditionTrue, reason: v1alpha1.ReasonConfigurationAdjustmentSucceeded},
				},
				{
					name: "rvr-2", nodeName: "node-2", rvrType: v1alpha1.ReplicaTypeDiskful,
					scheduled:  &testCondition{status: metav1.ConditionTrue, reason: "Scheduled"},
					configured: &testCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonConfigurationFailed},
				},
			},
			wantConfigured: &expectedCondition{status: metav1.ConditionFalse, reason: v1alpha1.ReasonReplicasNotConfigured, message: "1/2"},
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
	ctx := t.Context()
	s := setupScheme(t)

	// Create RV
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: tc.rvName,
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: tc.replicatedStorageClass,
		},
		Status: &v1alpha1.ReplicatedVolumeStatus{
			DRBD: &v1alpha1.DRBDResource{
				Config: &v1alpha1.DRBDResourceConfig{},
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
	builder := withRVRIndex(fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(rv, rsc).
		WithStatusSubresource(&v1alpha1.ReplicatedVolume{}, &v1alpha1.ReplicatedVolumeReplica{}))

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
	updatedRV := &v1alpha1.ReplicatedVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: tc.rvName}, updatedRV); err != nil {
		t.Fatalf("failed to get updated RV: %v", err)
	}

	// Check conditions
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVScheduled, tc.wantScheduled)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVBackingVolumeCreated, tc.wantBackingVolumeCreated)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVConfigured, tc.wantConfigured)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVInitialized, tc.wantInitialized)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVQuorum, tc.wantQuorum)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVDataQuorum, tc.wantDataQuorum)
	checkCondition(t, updatedRV.Status.Conditions, v1alpha1.ConditionTypeRVIOReady, tc.wantIOReady)

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
	if tc.wantAttachedAndIOReadyCount != "" {
		if updatedRV.Status.AttachedAndIOReadyCount != tc.wantAttachedAndIOReadyCount {
			t.Errorf("AttachedAndIOReadyCount: got %q, want %q", updatedRV.Status.AttachedAndIOReadyCount, tc.wantAttachedAndIOReadyCount)
		}
	}
}

func buildTestRVR(rvName string, spec testRVR) *v1alpha1.ReplicatedVolumeReplica {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: spec.name,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             spec.nodeName,
			Type:                 spec.rvrType,
		},
		Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{},
		},
	}

	addConditionIfSet(rvr, v1alpha1.ConditionTypeScheduled, spec.scheduled)
	addConditionIfSet(rvr, v1alpha1.ConditionTypeRVRBackingVolumeCreated, spec.backingVolumeCreated)
	addConditionIfSet(rvr, v1alpha1.ConditionTypeConfigurationAdjusted, spec.configured)
	addConditionIfSet(rvr, v1alpha1.ConditionTypeDataInitialized, spec.dataInitialized)
	addConditionIfSet(rvr, v1alpha1.ConditionTypeInQuorum, spec.inQuorum)
	addConditionIfSet(rvr, v1alpha1.ConditionTypeInSync, spec.inSync)
	addConditionIfSet(rvr, v1alpha1.ConditionTypeIOReady, spec.ioReady)

	return rvr
}

func addConditionIfSet(rvr *v1alpha1.ReplicatedVolumeReplica, condType string, cond *testCondition) {
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
