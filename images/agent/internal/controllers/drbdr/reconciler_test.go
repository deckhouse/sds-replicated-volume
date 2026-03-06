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

package drbdr_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
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
	// Helper to create configured status (resource exists with volume)
	configuredStatus := func(resName string) drbdsetup.StatusResult {
		return drbdsetup.StatusResult{
			{
				Name:   resName,
				NodeID: 0,
				Role:   "Secondary",
				Devices: []drbdsetup.Device{
					{Volume: 0, Minor: 1000, DiskState: "UpToDate", Quorum: true},
				},
			},
		}
	}

	testCases := []reconcileTestCase{
		// Basic cases - resource lookup
		{
			name:  "resource not found by name - returns done (no orphan cleanup)",
			drbdr: nil,
			// When reconcile is triggered by K8S name but object doesn't exist,
			// we just return Done (orphan cleanup requires ActualNameOnTheNode)
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{},
		},
		{
			name:  "resource belongs to different node - returns done (filtered by predicates)",
			drbdr: drbdrOnNode(testOtherNode, v1alpha1.DRBDResourceStateUp),
			// When reconcile is triggered for a resource on different node,
			// getDRBDRByName returns the object, but reconcileDRBDR checks
			// node ownership and returns done (predicates should filter this normally)
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{},
		},

		// State=Up cases - verifies correct drbdsetup command sequence
		{
			name:  "state up, drbd not configured - creates resource and minor",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(drbdsetup.StatusResult{}),
				// After status returns empty, actions are generated:
				// NewResourceAction calls new-resource
				newResourceCmd(testDRBDResName, 0),
				// ResourceOptionsAction sets resource options
				resourceOptionsCmd(testDRBDResName),
				// NewMinorAction calls ExecuteNewAutoMinor which tries nextDeviceMinor=0 first
				newMinorCmd(testDRBDResName, 0, 0, false),
				// After actions, refresh actual state
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(&drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options: drbdsetup.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
				}),
			},
			postCheck: func(t *testing.T, cl client.Client) {
				dr := &v1alpha1.DRBDResource{}
				if err := cl.Get(t.Context(), client.ObjectKey{Name: testDRBDRName}, dr); err != nil {
					t.Fatalf("failed to get DRBDResource: %v", err)
				}
				expectFinalizers(t, dr.Finalizers, v1alpha1.AgentFinalizer)
			},
		},
		{
			name:  "state up, drbd configured - queries status and show only",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(&drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options: drbdsetup.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
				}),
				// Resource exists and options match - no additional commands
			},
		},

		// State=Down cases
		{
			name:  "state down - queries status",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(drbdsetup.StatusResult{}),
			},
		},

		// Deletion cases
		{
			name:  "deleting resource - queries status",
			drbdr: deletingDRBDR(testNodeName, v1alpha1.AgentFinalizer),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(drbdsetup.StatusResult{}),
			},
		},

		// Custom DRBD resource name - triggers rename flow
		{
			name:  "custom drbd resource name - triggers rename to standard name",
			drbdr: drbdrWithCustomName(testNodeName, testCustomDRBDName),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				// When ActualNameOnTheNode is set, we rename from custom to standard name
				renameCmd(testCustomDRBDName, testDRBDResName),
			},
		},

		// Error cases - use State=Down to avoid action generation
		{
			name:  "status command error - returns error",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
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
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(drbdsetup.StatusResult{
					{Name: testDRBDResName, NodeID: 0, Role: "Secondary"},
				}),
				{
					Name:         drbdsetup.Command,
					Args:         drbdsetup.ShowArgs(testDRBDResName, true),
					ResultOutput: []byte("show error"),
					ResultErr:    fakedrbdsetup.ExitErr{Code: 1},
				},
			},
			expectedReconcileErr: "executing drbdsetup show",
		},
		{
			name:  "status exit code 10 - resource not found, creates resource",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				{
					Name:         drbdsetup.Command,
					Args:         drbdsetup.StatusArgs(testDRBDResName),
					ResultOutput: []byte(""),
					ResultErr:    fakedrbdsetup.ExitErr{Code: 10}, // Exit 10 = resource not found
				},
				// Resource not found triggers creation
				newResourceCmd(testDRBDResName, 0),
				// ResourceOptionsAction sets resource options
				resourceOptionsCmd(testDRBDResName),
				// ExecuteNewAutoMinor tries nextDeviceMinor=0 first
				newMinorCmd(testDRBDResName, 0, 0, false),
				// Refresh after actions
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(&drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options: drbdsetup.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
				}),
			},
		},

		// Maintenance mode - returns configured resource
		{
			name:  "maintenance mode - queries status and show",
			drbdr: drbdrInMaintenance(testNodeName, v1alpha1.MaintenanceModeNoResourceReconciliation),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(&drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options: drbdsetup.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
				}),
			},
		},

		// Multiple connections
		{
			name:  "resource with connections - removes stale peer",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdsetup.ExpectedCmd{
				statusCmd(drbdsetup.StatusResult{
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
				showCmd(&drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options: drbdsetup.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
					Connections: []drbdsetup.ShowConnection{
						{PeerNodeID: 1, Net: drbdsetup.ShowNet{Protocol: "C"}},
					},
				}),
				// Stale peer (in actual but not in intended spec): disconnect + del-peer + forget-peer
				disconnectCmd(testDRBDResName, 1),
				delPeerCmd(testDRBDResName, 1),
				forgetPeerCmd(testDRBDResName, 1),
				// Refresh after actions
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(&drbdsetup.ShowResource{
					Resource: testDRBDResName,
					Options: drbdsetup.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
				}),
			},
		},
	}

	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			drbdsetup.ResetNextDeviceMinor()
			drbdsetup.FlantExtensionsSupported = false

			// Build client with objects
			clientBuilder := fake.NewClientBuilder().
				WithScheme(sch).
				WithStatusSubresource(&v1alpha1.DRBDResource{}).
				WithIndex(&v1alpha1.DRBDResource{}, indexes.IndexFieldDRBDRByNodeName, func(obj client.Object) []string {
					dr, ok := obj.(*v1alpha1.DRBDResource)
					if !ok || dr.Spec.NodeName == "" {
						return nil
					}
					return []string{dr.Spec.NodeName}
				}).
				WithIndex(&snc.LVMVolumeGroup{}, indexes.IndexFieldLVGByNodeName, func(obj client.Object) []string {
					lvg, ok := obj.(*snc.LVMVolumeGroup)
					if !ok || len(lvg.Status.Nodes) == 0 {
						return nil
					}
					return []string{lvg.Status.Nodes[0].Name}
				}).
				WithIndex(&snc.LVMLogicalVolume{}, indexes.IndexFieldLLVByLVGName, func(obj client.Object) []string {
					llv, ok := obj.(*snc.LVMLogicalVolume)
					if !ok || llv.Spec.LVMVolumeGroupName == "" {
						return nil
					}
					return []string{llv.Spec.LVMVolumeGroupName}
				})

			objs := tc.objs
			if tc.drbdr != nil {
				objs = append([]client.Object{tc.drbdr}, objs...)
				// Add Node object for the DRBDResource's node
				objs = append(objs, testNode(tc.drbdr.Spec.NodeName))
			}
			if len(objs) > 0 {
				clientBuilder = clientBuilder.WithObjects(objs...)
			}
			cl := clientBuilder.Build()

			// Setup fake drbdsetup
			fakeExec := &fakedrbdsetup.Exec{}
			fakeExec.ExpectCommands(tc.expectedCommands...)
			fakeExec.Setup(t)

			// Create reconciler with port cache
			portCache := drbdr.NewPortCache(context.Background(), drbdr.PortRangeMin, drbdr.PortRangeMax)
			rec := drbdr.NewReconciler(cl, testNodeName, portCache)

			// Determine request name
			reqName := testDRBDRName
			if tc.drbdr != nil {
				reqName = tc.drbdr.Name
			}

			// Run reconcile
			_, err := rec.Reconcile(
				t.Context(),
				drbdr.DRBDReconcileRequest{Name: reqName},
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

func statusCmd(result drbdsetup.StatusResult) *fakedrbdsetup.ExpectedCmd {
	output, _ := json.Marshal(result)
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.StatusArgs(testDRBDResName),
		ResultOutput: output,
		ResultErr:    nil,
	}
}

func showCmd(result *drbdsetup.ShowResource) *fakedrbdsetup.ExpectedCmd {
	var results []drbdsetup.ShowResource
	if result != nil {
		results = []drbdsetup.ShowResource{*result}
	}
	output, _ := json.Marshal(results)
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.ShowArgs(testDRBDResName, true),
		ResultOutput: output,
		ResultErr:    nil,
	}
}

func newResourceCmd(resourceName string, nodeID uint8) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.NewResourceArgs(resourceName, nodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func newMinorCmd(resourceName string, minor, volume uint, diskless bool) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.NewMinorArgs(resourceName, minor, volume, diskless),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func disconnectCmd(resourceName string, peerNodeID uint8) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.DisconnectArgs(resourceName, peerNodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func delPeerCmd(resourceName string, peerNodeID uint8) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.DelPeerArgs(resourceName, peerNodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func forgetPeerCmd(resourceName string, peerNodeID uint8) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.ForgetPeerArgs(resourceName, peerNodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func downCmd(resourceName string) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.DownArgs(resourceName),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func renameCmd(oldName, newName string) *fakedrbdsetup.ExpectedCmd {
	return &fakedrbdsetup.ExpectedCmd{
		Name:         drbdsetup.Command,
		Args:         drbdsetup.RenameArgs(oldName, newName),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func resourceOptionsCmd(resourceName string) *fakedrbdsetup.ExpectedCmd {
	autoPromote := false
	quorum := uint(0)
	quorumMinRedundancy := uint(0)
	return &fakedrbdsetup.ExpectedCmd{
		Name: drbdsetup.Command,
		Args: drbdsetup.ResourceOptionsArgs(resourceName, drbdsetup.ResourceOptions{
			AutoPromote:                &autoPromote,
			OnNoQuorum:                 "suspend-io",
			OnNoDataAccessible:         "suspend-io",
			OnSuspendedPrimaryOutdated: "force-secondary",
			Quorum:                     &quorum,
			QuorumMinimumRedundancy:    &quorumMinRedundancy,
		}),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
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

func mustParseQuantity(s string) *resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return &q
}

func testNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
			},
		},
	}
}
