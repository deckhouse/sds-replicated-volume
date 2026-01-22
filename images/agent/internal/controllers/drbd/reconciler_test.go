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

package drbd_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	fakedrbdsetup "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup/fake"
)

const (
	testNodeName       = "test-node"
	testOtherNode      = "other-node"
	testDRBDRName      = "test-drbdr"
	testDRBDResName    = "sdsrv-" + testDRBDRName
	testCustomDRBDName = "custom-drbd-name"
)

type reconcileTestCase struct {
	name string

	// Input: DRBDResource to reconcile
	drbdr *v1alpha1.DRBDResource

	// Additional objects in the cluster
	objs []client.Object

	// Expected drbdsetup commands (in order)
	expectedCommands []*fakedrbdsetup.ExpectedCmd

	// Expected reconcile error (nil means no error expected)
	expectedReconcileErr string

	// Post-reconcile checks
	postCheck func(t *testing.T, cl client.Client)
}

func TestReconciler_Reconcile(t *testing.T) {
	testCases := []reconcileTestCase{
		// Basic cases - resource lookup
		{
			name:             "resource not found - no action",
			drbdr:            nil,
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{},
		},
		{
			name:             "resource belongs to different node - skip",
			drbdr:            drbdrOnNode(testOtherNode, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{},
		},

		// State=Up cases - verifies correct drbdsetup command sequence
		{
			name:  "state up, drbd not configured - queries status only",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{}),
			},
		},
		{
			name:  "state up, drbd configured - queries status and show",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{
					{
						Name:   testDRBDResName,
						NodeID: 0,
						Role:   "Secondary",
						Devices: []drbdsetup.Device{
							{Volume: 0, Minor: 1000, DiskState: "UpToDate", Quorum: true},
						},
					},
				}),
				showCmd(testDRBDResName, &drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options:  drbdsetup.ShowOptions{Quorum: "2", QuorumMinimumRedundancy: "1"},
				}),
			},
		},

		// State=Down cases
		{
			name:  "state down - queries status",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{}),
			},
		},

		// Deletion cases
		{
			name:  "deleting resource - queries status",
			drbdr: deletingDRBDR(testNodeName, v1alpha1.AgentFinalizer),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{}),
			},
		},

		// Custom DRBD resource name
		{
			name:  "custom drbd resource name - uses actualNameOnTheNode for drbdsetup",
			drbdr: drbdrWithCustomName(testNodeName, testCustomDRBDName),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testCustomDRBDName, drbdsetup.StatusResult{}),
			},
		},

		// Error cases
		{
			name:  "status command error - returns error",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				{
					Name:         drbdsetup.Command,
					Args:         drbdsetup.StatusArgs(testDRBDResName),
					ResultOutput: []byte("error output"),
					ResultErr:    fakedrbdsetup.ExitErr{Code: 1},
				},
			},
			expectedReconcileErr: "executing drbdsetup status",
		},
		{
			name:  "show command error - returns error",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{
					{Name: testDRBDResName, NodeID: 0, Role: "Secondary"},
				}),
				{
					Name:         drbdsetup.Command,
					Args:         drbdsetup.ShowArgs(testDRBDResName),
					ResultOutput: []byte("show error"),
					ResultErr:    fakedrbdsetup.ExitErr{Code: 1},
				},
			},
			expectedReconcileErr: "executing drbdsetup show",
		},
		{
			name:  "status exit code 10 - resource not found, no error",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				{
					Name:         drbdsetup.Command,
					Args:         drbdsetup.StatusArgs(testDRBDResName),
					ResultOutput: []byte(""),
					ResultErr:    fakedrbdsetup.ExitErr{Code: 10}, // Exit 10 = resource not found
				},
			},
			// No error expected - exit code 10 means resource not found
		},

		// Maintenance mode
		{
			name:  "maintenance mode - queries status and show",
			drbdr: drbdrInMaintenance(testNodeName, v1alpha1.MaintenanceModeNoResourceReconciliation),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{
					{Name: testDRBDResName, NodeID: 0, Role: "Primary"},
				}),
				showCmd(testDRBDResName, &drbdsetup.ShowResource{Resource: testDRBDResName}),
			},
		},

		// Multiple connections
		{
			name:  "resource with connections - queries status and show",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(testDRBDResName, drbdsetup.StatusResult{
					{
						Name:   testDRBDResName,
						NodeID: 0,
						Role:   "Primary",
						Devices: []drbdsetup.Device{
							{Volume: 0, Minor: 1000, DiskState: "UpToDate", Quorum: true},
						},
						Connections: []drbdsetup.Connection{
							{
								PeerNodeID:      1,
								Name:            "peer-1",
								ConnectionState: "Connected",
								PeerDevices: []drbdsetup.PeerDevice{
									{Volume: 0, ReplicationState: "Established", PeerDiskState: "UpToDate"},
								},
							},
						},
					},
				}),
				showCmd(testDRBDResName, &drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options:  drbdsetup.ShowOptions{Quorum: "2"},
					Connections: []drbdsetup.ShowConnection{
						{PeerNodeID: 1, Net: drbdsetup.ShowNet{Protocol: "C"}},
					},
				}),
			},
		},
	}

	setupDiscardLogger(t)

	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Build client with objects
			clientBuilder := fake.NewClientBuilder().
				WithScheme(sch).
				WithStatusSubresource(&v1alpha1.DRBDResource{})

			objs := tc.objs
			if tc.drbdr != nil {
				objs = append([]client.Object{tc.drbdr}, objs...)
			}
			if len(objs) > 0 {
				clientBuilder = clientBuilder.WithObjects(objs...)
			}
			cl := clientBuilder.Build()

			// Setup fake drbdsetup
			fakeExec := &fakedrbdsetup.Exec{}
			fakeExec.ExpectCommands(tc.expectedCommands...)
			fakeExec.Setup(t)

			// Create reconciler
			rec := drbd.NewReconciler(cl, nil, testNodeName)

			// Determine request name
			reqName := testDRBDRName
			if tc.drbdr != nil {
				reqName = tc.drbdr.Name
			}

			// Run reconcile
			_, err := rec.Reconcile(
				t.Context(),
				reconcile.Request{
					NamespacedName: types.NamespacedName{Name: reqName},
				},
			)

			// Check error
			if tc.expectedReconcileErr != "" {
				if err == nil {
					t.Errorf("expected reconcile error containing %q, got nil", tc.expectedReconcileErr)
				} else if !strings.Contains(err.Error(), tc.expectedReconcileErr) {
					t.Errorf("expected reconcile error containing %q, got %q", tc.expectedReconcileErr, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected reconcile error: %v", err)
			}

			// Run post-check
			if tc.postCheck != nil {
				tc.postCheck(t, cl)
			}
		})
	}
}

// Helper functions

func setupDiscardLogger(t *testing.T) {
	t.Helper()
	prevLogger := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
	})
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func baseDRBDR() *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testDRBDRName,
			Generation: 1,
		},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName:       testNodeName,
			State:          v1alpha1.DRBDResourceStateUp,
			SystemNetworks: []string{"default"},
			Size:           mustParseQuantity("1Gi"),
			NodeID:         0,
		},
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.DRBDResourceCondConfiguredType,
					Status:             metav1.ConditionTrue,
					Reason:             v1alpha1.DRBDResourceCondConfiguredReasonConfigured,
					LastTransitionTime: metav1.Now(),
					ObservedGeneration: 0,
				},
			},
		},
	}
}

func drbdrOnNode(nodeName string, state v1alpha1.DRBDResourceState) *v1alpha1.DRBDResource {
	dr := baseDRBDR()
	dr.Spec.NodeName = nodeName
	dr.Spec.State = state
	return dr
}

func drbdrWithCustomName(nodeName, customName string) *v1alpha1.DRBDResource {
	dr := baseDRBDR()
	dr.Spec.NodeName = nodeName
	dr.Spec.ActualNameOnTheNode = customName
	return dr
}

func deletingDRBDR(nodeName string, finalizers ...string) *v1alpha1.DRBDResource {
	dr := baseDRBDR()
	dr.Spec.NodeName = nodeName
	dr.Finalizers = finalizers
	now := metav1.NewTime(time.Now())
	dr.DeletionTimestamp = &now
	return dr
}

func drbdrInMaintenance(nodeName string, mode v1alpha1.MaintenanceMode) *v1alpha1.DRBDResource {
	dr := baseDRBDR()
	dr.Spec.NodeName = nodeName
	dr.Spec.Maintenance = mode
	return dr
}

func statusCmd(resourceName string, result drbdsetup.StatusResult) *fakedrbdsetup.ExpectedCmd {
	output, _ := json.Marshal(result)
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.StatusArgs(resourceName),
		ResultOutput: output,
		ResultErr:    nil,
	}
}

func showCmd(resourceName string, result *drbdsetup.ShowResource) *fakedrbdsetup.ExpectedCmd {
	var results []drbdsetup.ShowResource
	if result != nil {
		results = []drbdsetup.ShowResource{*result}
	}
	output, _ := json.Marshal(results)
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.ShowArgs(resourceName),
		ResultOutput: output,
		ResultErr:    nil,
	}
}

func fetchDRBDR(t *testing.T, cl client.Client, name string) *v1alpha1.DRBDResource {
	t.Helper()
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: name}, drbdr); err != nil {
		t.Fatalf("getting DRBDResource %s: %v", name, err)
	}
	return drbdr
}

func expectFinalizers(t *testing.T, got []string, expected ...string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("finalizers mismatch: got %v, expected %v", got, expected)
	}
	for _, exp := range expected {
		found := false
		for _, g := range got {
			if g == exp {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("finalizer %s not found in %v", exp, got)
		}
	}
}

func mustParseQuantity(s string) (q resource.Quantity) {
	q, _ = resource.ParseQuantity(s)
	return q
}
