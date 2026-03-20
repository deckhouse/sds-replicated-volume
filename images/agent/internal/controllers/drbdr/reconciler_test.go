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
	"fmt"
	"os"
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
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	fakedrbdutils "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils/fake"
)

const (
	testNodeName       = "test-node"
	testOtherNode      = "other-node"
	testDRBDRName      = "test-drbdr"
	testDRBDResName    = "sdsrv-" + testDRBDRName
	testCustomDRBDName = "custom-drbd-name"
	testLLVName        = "test-llv"
)

type reconcileTestCase struct {
	name string

	// Input: DRBDResource to reconcile
	drbdr *v1alpha1.DRBDResource

	// Additional objects in the cluster
	objs []client.Object

	// Override reconcile request (default: {Name: drbdr.Name or testDRBDRName})
	request *drbdr.DRBDReconcileRequest

	// Expected drbdsetup commands (in order)
	expectedCommands []*fakedrbdutils.ExpectedCmd

	// Expected reconcile error (nil means no error expected)
	expectedReconcileErr string

	// Post-reconcile checks
	postCheck func(t *testing.T, cl client.Client)
}

func TestReconciler_Reconcile(t *testing.T) {
	configuredStatus := func(resName string) drbdutils.StatusResult {
		return drbdutils.StatusResult{
			{
				Name:   resName,
				NodeID: 0,
				Role:   "Secondary",
				Devices: []drbdutils.Device{
					{Volume: 0, Minor: 1000, DiskState: "UpToDate", Quorum: true, Size: 1048316},
				},
			},
		}
	}

	configuredShow := func(resName string) *drbdutils.ShowResource {
		return &drbdutils.ShowResource{
			Resource: resName,
			Options: drbdutils.ShowOptions{
				AutoPromote:                false,
				Quorum:                     "off",
				QuorumMinimumRedundancy:    "off",
				OnNoQuorum:                 "suspend-io",
				OnNoDataAccessible:         "suspend-io",
				OnSuspendedPrimaryOutdated: "force-secondary",
			},
		}
	}

	testCases := []reconcileTestCase{
		// Basic cases - resource lookup
		{
			name:  "resource not found by name, no DRBD on node - done",
			drbdr: nil,
			// K8S object doesn't exist; orphan check queries DRBD status
			// and finds nothing on the node.
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{}),
			},
		},
		{
			name:  "resource not found by name, DRBD exists on node - orphan cleanup",
			drbdr: nil,
			// K8S object doesn't exist but DRBD resource is running on the
			// node. The reconciler tears it down.
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(configuredStatus(testDRBDResName)),
				downCmd(testDRBDResName),
			},
		},
		{
			name: "non-prefixed DRBD with no owner - skipped",
			// No K8S objects exist, non-prefixed DRBD name has no owner.
			request:          &drbdr.DRBDReconcileRequest{ActualNameOnTheNode: "foreign-resource"},
			expectedCommands: []*fakedrbdutils.ExpectedCmd{},
		},
		{
			name:  "resource belongs to different node - returns done (filtered by predicates)",
			drbdr: drbdrOnNode(testOtherNode, v1alpha1.DRBDResourceStateUp),
			// When reconcile is triggered for a resource on different node,
			// getDRBDRByName returns the object, but reconcileDRBDR checks
			// node ownership and returns done (predicates should filter this normally)
			expectedCommands: []*fakedrbdutils.ExpectedCmd{},
		},

		// State=Up cases - verifies correct drbdsetup command sequence
		{
			name:  "state up, drbd not configured - creates resource and minor",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{}),
				// After status returns empty, actions are generated:
				// NewResourceAction calls new-resource
				newResourceCmd(testDRBDResName, 0),
				// ResourceOptionsAction sets resource options
				resourceOptionsCmd(testDRBDResName),
				// NewMinorAction calls ExecuteNewAutoMinor which tries nextDeviceMinor=0 first
				newMinorCmd(testDRBDResName, 0, 0, false),
				// EnsureDeviceSymlinkAction runs (filesystem, not drbdsetup)
				// After actions, refresh actual state
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(configuredShow(testDRBDResName)),
			},
			postCheck: func(t *testing.T, cl client.Client) {
				dr := &v1alpha1.DRBDResource{}
				if err := cl.Get(t.Context(), client.ObjectKey{Name: testDRBDRName}, dr); err != nil {
					t.Fatalf("failed to get DRBDResource: %v", err)
				}
				expectFinalizers(t, dr.Finalizers, v1alpha1.AgentFinalizer)
				expectDeviceSymlink(t, testDRBDRName, 0)
				if dr.Status.Device != drbdr.DeviceSymlinkPath(testDRBDRName) {
					t.Errorf("status.device = %q, want %q", dr.Status.Device, drbdr.DeviceSymlinkPath(testDRBDRName))
				}

				// status.size = DRBD usable size (Device.Size KiB * 1024)
				wantUsableSize := resource.NewQuantity(1048316*1024, resource.BinarySI)
				if dr.Status.Size == nil {
					t.Error("status.size is nil, want non-nil")
				} else if !dr.Status.Size.Equal(*wantUsableSize) {
					t.Errorf("status.size = %s, want %s", dr.Status.Size.String(), wantUsableSize.String())
				}
			},
		},
		{
			name:  "state up, drbd configured - queries status and show only",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(configuredShow(testDRBDResName)),
				// Resource exists and options match - EnsureDeviceSymlinkAction runs (filesystem only)
				// Refresh after symlink action
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(configuredShow(testDRBDResName)),
			},
			postCheck: func(t *testing.T, _ client.Client) {
				expectDeviceSymlink(t, testDRBDRName, 1000)
			},
		},

		// State=Down cases
		{
			name:  "state down - queries status",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{}),
				// RemoveDeviceSymlinkAction runs (filesystem only)
				// Refresh after symlink action
				statusCmd(drbdutils.StatusResult{}),
			},
		},
		{
			name:  "state down diskful - releases LLV finalizer",
			drbdr: drbdrDiskfulDown(testNodeName, testLLVName, testLLVName),
			objs:  []client.Object{testLLVWithFinalizer(testLLVName)},
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{}),
				statusCmd(drbdutils.StatusResult{}),
			},
			postCheck: func(t *testing.T, cl client.Client) {
				expectLLVHasNoAgentFinalizer(t, cl, testLLVName)
			},
		},
		{
			name:  "state down diskful - empty pending list means nothing to release",
			drbdr: drbdrDiskfulDown(testNodeName, testLLVName, ""),
			objs:  []client.Object{testLLVWithFinalizer(testLLVName)},
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{}),
				statusCmd(drbdutils.StatusResult{}),
			},
			postCheck: func(t *testing.T, cl client.Client) {
				// No LLV in the pending-release list → nothing to release.
				// Recovery would catch this if DRBD had the disk attached.
				expectLLVHasAgentFinalizer(t, cl, testLLVName)
			},
		},

		// Deletion cases
		{
			name:  "deleting resource - queries status",
			drbdr: deletingDRBDR(testNodeName, v1alpha1.AgentFinalizer),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{}),
				// RemoveDeviceSymlinkAction runs (filesystem only)
				// Refresh after symlink action
				statusCmd(drbdutils.StatusResult{}),
			},
		},

		// Custom DRBD resource name - triggers rename flow
		{
			name:  "custom drbd resource name - triggers rename to standard name",
			drbdr: drbdrWithCustomName(testNodeName, testCustomDRBDName),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				// When ActualNameOnTheNode is set, we rename from custom to standard name
				renameCmd(testCustomDRBDName, testDRBDResName),
			},
		},

		// Custom DRBD resource name + maintenance mode - rename is skipped
		{
			name:  "custom drbd resource name in maintenance mode - skips rename",
			drbdr: drbdrWithCustomNameInMaintenance(testNodeName, testCustomDRBDName),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				// Phase 0 rename is skipped due to MM; falls through to Phase 2+4.
				// Status and show use the custom (old) name via DRBDResourceNameOnTheNode.
				{
					Name:         drbdutils.DRBDSetupCommand,
					Args:         drbdutils.StatusArgs(testCustomDRBDName),
					ResultOutput: mustJSON(configuredStatus(testCustomDRBDName)),
				},
				{
					Name:         drbdutils.DRBDSetupCommand,
					Args:         drbdutils.ShowArgs(testCustomDRBDName, true),
					ResultOutput: mustJSON([]drbdutils.ShowResource{*configuredShow(testCustomDRBDName)}),
				},
				// Maintenance mode skips all actions
			},
		},

		// Error cases - use State=Down to avoid action generation
		{
			name:  "status command error - returns error",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				{
					Name:         drbdutils.DRBDSetupCommand,
					Args:         drbdutils.StatusArgs(testDRBDResName),
					ResultOutput: []byte("error output"),
					ResultErr:    fakedrbdutils.ExitErr{Code: 1},
				},
			},
			expectedReconcileErr: "executing drbdsetup status",
		},
		{
			name:  "show command error - returns error",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateDown),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{
					{Name: testDRBDResName, NodeID: 0, Role: "Secondary"},
				}),
				{
					Name:         drbdutils.DRBDSetupCommand,
					Args:         drbdutils.ShowArgs(testDRBDResName, true),
					ResultOutput: []byte("show error"),
					ResultErr:    fakedrbdutils.ExitErr{Code: 1},
				},
			},
			expectedReconcileErr: "executing drbdsetup show",
		},
		{
			name:  "status exit code 10 - resource not found, creates resource",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				{
					Name:         drbdutils.DRBDSetupCommand,
					Args:         drbdutils.StatusArgs(testDRBDResName),
					ResultOutput: []byte(testDRBDResName + ": No such resource\n"),
					ResultErr:    fakedrbdutils.ExitErr{Code: 10},
				},
				// Resource not found triggers creation
				newResourceCmd(testDRBDResName, 0),
				// ResourceOptionsAction sets resource options
				resourceOptionsCmd(testDRBDResName),
				// ExecuteNewAutoMinor tries nextDeviceMinor=0 first
				newMinorCmd(testDRBDResName, 0, 0, false),
				// EnsureDeviceSymlinkAction runs (filesystem, not drbdsetup)
				// Refresh after actions
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(configuredShow(testDRBDResName)),
			},
		},

		// Maintenance mode - returns configured resource
		{
			name:  "maintenance mode - queries status and show",
			drbdr: drbdrInMaintenance(testNodeName, v1alpha1.MaintenanceModeNoResourceReconciliation),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(configuredShow(testDRBDResName)),
				// Maintenance mode skips all actions (including EnsureDeviceSymlinkAction)
			},
		},

		// Multiple connections
		{
			name:  "resource with connections - removes stale peer",
			drbdr: drbdrOnNode(testNodeName, v1alpha1.DRBDResourceStateUp),
			expectedCommands: []*fakedrbdutils.ExpectedCmd{
				statusCmd(drbdutils.StatusResult{
					{
						Name:   testDRBDResName,
						NodeID: 0,
						Role:   "Primary",
						Devices: []drbdutils.Device{
							{Volume: 0, Minor: 1000, DiskState: "UpToDate", Quorum: true},
						},
						Connections: []drbdutils.Connection{
							{
								PeerNodeID:      1,
								Name:            "peer-1",
								ConnectionState: "Connected",
								PeerDevices: []drbdutils.PeerDevice{
									{Volume: 0, ReplicationState: "Established", PeerDiskState: "UpToDate"},
								},
							},
						},
					},
				}),
				showCmd(&drbdutils.ShowResource{
					Resource: testDRBDResName,
					Options: drbdutils.ShowOptions{
						AutoPromote:                false,
						Quorum:                     "off",
						QuorumMinimumRedundancy:    "off",
						OnNoQuorum:                 "suspend-io",
						OnNoDataAccessible:         "suspend-io",
						OnSuspendedPrimaryOutdated: "force-secondary",
					},
					Connections: []drbdutils.ShowConnection{
						{PeerNodeID: 1, Net: drbdutils.ShowNet{Protocol: "C"}},
					},
				}),
				// EnsureDeviceSymlinkAction runs (filesystem, not drbdsetup)
				// Stale peer (in actual but not in intended spec): disconnect + del-peer + forget-peer
				disconnectCmd(testDRBDResName, 1),
				delPeerCmd(testDRBDResName, 1),
				forgetPeerCmd(testDRBDResName, 1),
				// Refresh after actions
				statusCmd(configuredStatus(testDRBDResName)),
				showCmd(configuredShow(testDRBDResName)),
			},
		},
	}

	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			drbdutils.ResetNextDeviceMinor()
			drbdutils.FlantExtensionsSupported = false

			symlinkDir := t.TempDir() + "/"
			drbdr.OverrideDeviceSymlinkDir(symlinkDir)

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
			fakeExec := &fakedrbdutils.Exec{}
			fakeExec.ExpectCommands(tc.expectedCommands...)
			fakeExec.Setup(t)

			// Create reconciler with port cache
			portCache := drbdr.NewPortCache(context.Background(), drbdr.PortRangeMin, drbdr.PortRangeMax)
			rec := drbdr.NewReconciler(cl, testNodeName, portCache)

			// Build reconcile request
			var req drbdr.DRBDReconcileRequest
			if tc.request != nil {
				req = *tc.request
			} else {
				reqName := testDRBDRName
				if tc.drbdr != nil {
					reqName = tc.drbdr.Name
				}
				req = drbdr.DRBDReconcileRequest{Name: reqName}
			}

			// Run reconcile
			_, err := rec.Reconcile(t.Context(), req)

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

func drbdrWithCustomNameInMaintenance(nodeName, customName string) *v1alpha1.DRBDResource {
	dr := baseDRBDR()
	dr.Spec.NodeName = nodeName
	dr.Spec.ActualNameOnTheNode = customName
	dr.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
	return dr
}

func statusCmd(result drbdutils.StatusResult) *fakedrbdutils.ExpectedCmd {
	output, _ := json.Marshal(result)
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.StatusArgs(testDRBDResName),
		ResultOutput: output,
		ResultErr:    nil,
	}
}

func showCmd(result *drbdutils.ShowResource) *fakedrbdutils.ExpectedCmd {
	var results []drbdutils.ShowResource
	if result != nil {
		results = []drbdutils.ShowResource{*result}
	}
	output, _ := json.Marshal(results)
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.ShowArgs(testDRBDResName, true),
		ResultOutput: output,
		ResultErr:    nil,
	}
}

func newResourceCmd(resourceName string, nodeID uint8) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.NewResourceArgs(resourceName, nodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func newMinorCmd(resourceName string, minor, volume uint, diskless bool) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.NewMinorArgs(resourceName, minor, volume, diskless),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func disconnectCmd(resourceName string, peerNodeID uint8) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.DisconnectArgs(resourceName, peerNodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func delPeerCmd(resourceName string, peerNodeID uint8) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.DelPeerArgs(resourceName, peerNodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func forgetPeerCmd(resourceName string, peerNodeID uint8) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.ForgetPeerArgs(resourceName, peerNodeID),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func downCmd(resourceName string) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.DownArgs(resourceName),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func renameCmd(oldName, newName string) *fakedrbdutils.ExpectedCmd {
	return &fakedrbdutils.ExpectedCmd{
		Name:         drbdutils.DRBDSetupCommand,
		Args:         drbdutils.RenameArgs(oldName, newName),
		ResultOutput: []byte{},
		ResultErr:    nil,
	}
}

func resourceOptionsCmd(resourceName string) *fakedrbdutils.ExpectedCmd {
	autoPromote := false
	quorum := uint(0)
	quorumMinRedundancy := uint(0)
	return &fakedrbdutils.ExpectedCmd{
		Name: drbdutils.DRBDSetupCommand,
		Args: drbdutils.ResourceOptionsArgs(resourceName, drbdutils.ResourceOptions{
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

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
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

func expectDeviceSymlink(t *testing.T, k8sName string, expectedMinor uint) {
	t.Helper()
	symlinkPath := drbdr.DeviceSymlinkPath(k8sName)
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", symlinkPath, err)
	}
	wantTarget := fmt.Sprintf("/dev/drbd%d", expectedMinor)
	if target != wantTarget {
		t.Errorf("symlink %s -> %s, want -> %s", symlinkPath, target, wantTarget)
	}
}

func drbdrDiskfulDown(nodeName, specLLV, statusLLV string) *v1alpha1.DRBDResource {
	dr := baseDRBDR()
	dr.Spec.NodeName = nodeName
	dr.Spec.State = v1alpha1.DRBDResourceStateDown
	dr.Spec.Type = v1alpha1.DRBDResourceTypeDiskful
	dr.Spec.LVMLogicalVolumeName = specLLV
	if statusLLV != "" {
		dr.Status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{
			LVMLogicalVolumeName:   statusLLV,
			LLVFinalizersToRelease: []string{statusLLV},
		}
	}
	return dr
}

func testLLVWithFinalizer(name string) *snc.LVMLogicalVolume {
	return &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{v1alpha1.AgentFinalizer},
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: name,
			Type:                  "Thick",
			Size:                  "100Mi",
			LVMVolumeGroupName:    "test-lvg",
		},
	}
}

func expectLLVHasAgentFinalizer(t *testing.T, cl client.Client, llvName string) {
	t.Helper()
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(t.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
		t.Fatalf("getting LLV %q: %v", llvName, err)
	}
	for _, f := range llv.Finalizers {
		if f == v1alpha1.AgentFinalizer {
			return
		}
	}
	t.Errorf("LLV %q does not have agent finalizer %q", llvName, v1alpha1.AgentFinalizer)
}

func expectLLVHasNoAgentFinalizer(t *testing.T, cl client.Client, llvName string) {
	t.Helper()
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(t.Context(), client.ObjectKey{Name: llvName}, llv); err != nil {
		t.Fatalf("getting LLV %q: %v", llvName, err)
	}
	for _, f := range llv.Finalizers {
		if f == v1alpha1.AgentFinalizer {
			t.Errorf("LLV %q still has agent finalizer %q", llvName, v1alpha1.AgentFinalizer)
			return
		}
	}
}
