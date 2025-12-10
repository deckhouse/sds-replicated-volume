package drbdconfig_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	u "github.com/deckhouse/sds-common-lib/utils"
	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	drbdadmfake "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm/fake"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	drbdconfig "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd_config"
	agentscheme "github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
)

const testNodeName = "testNodeName"

const (
	testRVUp         = "rv-up"
	testRVRUp        = "rvr-up"
	testRVDown       = "rv-down"
	testRVRDown      = "rvr-down"
	testRVDiskful    = "rv-up-diskful"
	testRVRDiskful   = "rvr-up-diskful"
	testLvgDiskful   = "lvg-up"
	testLlvDiskful   = "llv-up"
	testSharedRVSSA  = "rv-ssa"
	testSharedRVRSSA = "rvr-ssa"
	testRVNoCfgOld   = "rv-no-config-old"
	testRVNoCfg      = "rv-no-config"
	testRVAdd        = "rv-add"
	testRVSame       = "rv-same"
	testRVChange     = "rv-change"
	testRVOther      = "rv-other"
	testRVNonAgent   = "rv-non-agent"
	testRVMissing    = "rv-missing"
	testRVNotInit    = "rv-not-init"
	testRVRNotInit   = "rvr-not-init"
	testRVRNonAgent  = "rvr-non-agent"
	testRVRMissing   = "rvr-no-rv"
	testRVROther     = "rvr-other"
	testLLVDown      = "llv-down"
)

type onRVUpdateTestCase struct {
	name string
	//
	oldRV, newRV *v1alpha3.ReplicatedVolume
	//
	expectedQueueItems []drbdconfig.Request
}

type onRVRCreateOrUpdateTestCase struct {
	name string
	//
	rvr *v1alpha3.ReplicatedVolumeReplica
	//
	expectedQueueItems []drbdconfig.Request
	addRV              *bool
}

type reconcileTestCase struct {
	name string
	//
	request                 drbdconfig.Request
	existingObjects         []client.Object
	fsSetup                 func()
	expectSharedSecretError *bool
	//
	assertErr func(*testing.T, error)
}

// SetFSForTests replaces filesystem for tests and returns a restore function.
// Production keeps OS-backed fs; tests swap it to memory/fs mocks.
func setupMemFS(t *testing.T) {
	t.Helper()
	prevAfs := drbdconfig.FS
	t.Cleanup(func() { drbdconfig.FS = prevAfs })
	drbdconfig.FS = &afero.Afero{Fs: afero.NewMemMapFs()}
}

func setupDiscardLogger(t *testing.T) {
	t.Helper()
	prevLogger := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
	})
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestReconciler_OnRVUpdate(t *testing.T) {
	setupDiscardLogger(t)

	tests := []onRVUpdateTestCase{
		{
			name: "new rv without drbd config",
			oldRV: &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: testRVNoCfgOld},
			},
			newRV: &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: testRVNoCfg},
			},
		},
		{
			name:  "shared secret alg added",
			oldRV: rvWithSharedSecretAlg(testRVAdd, ""),
			newRV: rvWithSharedSecretAlg(testRVAdd, "sha256"),
			expectedQueueItems: []drbdconfig.Request{
				drbdconfig.SharedSecretAlgRequest{
					RVName:          testRVAdd,
					SharedSecretAlg: "sha256",
				},
			},
		},
		{
			name:  "shared secret alg unchanged",
			oldRV: rvWithSharedSecretAlg(testRVSame, "sha256"),
			newRV: rvWithSharedSecretAlg(testRVSame, "sha256"),
		},
		{
			name:  "shared secret alg changed",
			oldRV: rvWithSharedSecretAlg(testRVChange, "sha1"),
			newRV: rvWithSharedSecretAlg(testRVChange, "sha256"),
			expectedQueueItems: []drbdconfig.Request{
				drbdconfig.SharedSecretAlgRequest{
					RVName:          testRVChange,
					SharedSecretAlg: "sha256",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[drbdconfig.TReq]())
			defer q.ShutDown()

			rec := drbdconfig.NewReconciler(nil, nil, slog.Default(), testNodeName)

			rec.OnRVUpdate(t.Context(), tc.oldRV, tc.newRV, q)

			if got := q.Len(); got != len(tc.expectedQueueItems) {
				t.Fatalf("expected queue len %d, got %d", len(tc.expectedQueueItems), got)
			}
			for _, expectedReq := range tc.expectedQueueItems {
				item, _ := q.Get()

				if diff := cmp.Diff(expectedReq, item, cmpopts.IgnoreUnexported(drbdconfig.UpRequest{}, drbdconfig.DownRequest{})); diff != "" {
					t.Fatalf("mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestReconciler_OnRVRCreateOrUpdate(t *testing.T) {
	setupDiscardLogger(t)

	scheme, err := agentscheme.New()
	if err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	tests := []onRVRCreateOrUpdateTestCase{
		{
			name: "rvr on another node",
			rvr: newRVR(testRVROther, testRVOther, "Access",
				withRVRNode("other")),
		},
		{
			name: "deletion with non-agent finalizer",
			rvr: newRVR(testRVRNonAgent, testRVNonAgent, "Access",
				withRVRDeletion(),
				withRVRNode(testNodeName),
				withRVRFinalizers("something")),
		},
		{
			name: "deletion with agent finalizer enqueues down",
			rvr: newRVR(testRVRDown, testRVDown, "Access",
				withRVRDeletion(),
				withRVRNode(testNodeName),
				withRVRFinalizers(v1alpha3.AgentAppFinalizer)),
			expectedQueueItems: []drbdconfig.Request{
				drbdconfig.DownRequest{testRVRDown},
			},
		},
		{
			name: "rv missing in api",
			rvr: newRVR(testRVRMissing, testRVMissing, "Access",
				withRVRNode(testNodeName),
				withRVRStatus(minimalRVRStatus())),
			addRV: u.Ptr(false),
		},
		{
			name: "rvr not initialized yet",
			rvr: newRVR(testRVRNotInit, testRVNotInit, "Access",
				withRVRNode(testNodeName)),
		},
		{
			name: "rvr initialized enqueues up",
			rvr: newRVR(testRVRUp, testRVUp, "Access",
				withRVRNode(testNodeName),
				withRVRStatus(minimalRVRStatus())),
			expectedQueueItems: []drbdconfig.Request{
				drbdconfig.UpRequest{testRVRUp},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)

			// Add RV if needed
			if tc.rvr != nil && (tc.addRV == nil || *tc.addRV) {
				rv := rvWithSharedSecretAlg(tc.rvr.Spec.ReplicatedVolumeName, "sha256")
				builder = builder.WithRuntimeObjects(rv)
			}
			cl := builder.Build()

			q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[drbdconfig.TReq]())
			defer q.ShutDown()

			rec := drbdconfig.NewReconciler(cl, cl, slog.Default(), testNodeName)

			rec.OnRVRCreateOrUpdate(t.Context(), tc.rvr, q)

			if got := q.Len(); got != len(tc.expectedQueueItems) {
				t.Fatalf("expected queue len %d, got %d", len(tc.expectedQueueItems), got)
			}
			for _, expectedReq := range tc.expectedQueueItems {
				item, _ := q.Get()

				if diff := cmp.Diff(expectedReq, item, cmpopts.IgnoreUnexported(drbdconfig.UpRequest{}, drbdconfig.DownRequest{})); diff != "" {
					t.Fatalf("mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestReconciler_Reconcile(t *testing.T) {
	setupDiscardLogger(t)

	tests := []reconcileTestCase{
		{
			name:    "shared secret alg unsupported gets patched",
			request: drbdconfig.SharedSecretAlgRequest{testSharedRVSSA, "sha256"},
			existingObjects: []client.Object{
				newRVR(testSharedRVRSSA, testSharedRVSSA, "Access",
					withRVRNode(testNodeName),
					withRVRStatus(&v1alpha3.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha3.DRBD{
							Errors: &v1alpha3.DRBDErrors{},
						},
					}),
				),
			},
			fsSetup: func() {
				_ = drbdconfig.FS.MkdirAll("/proc", 0755)
				// empty file -> alg not supported
				_, _ = drbdconfig.FS.Create("/proc/crypto")
			},
			expectSharedSecretError: u.Ptr(true),
			assertErr: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			},
		},
		{
			name:    "shared secret alg supported keeps status intact",
			request: drbdconfig.SharedSecretAlgRequest{testSharedRVSSA, "sha256"},
			existingObjects: []client.Object{
				newRVR(testSharedRVRSSA, testSharedRVSSA, "Access",
					withRVRNode(testNodeName),
					withRVRStatus(&v1alpha3.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha3.DRBD{
							Errors: &v1alpha3.DRBDErrors{},
						},
					}),
				),
			},
			fsSetup: func() {
				_ = drbdconfig.FS.MkdirAll("/proc", 0755)
				f, _ := drbdconfig.FS.Create("/proc/crypto")
				defer f.Close()
				_, _ = f.WriteString("name         : sha256\n\n")
			},
			expectSharedSecretError: u.Ptr(false),
			assertErr: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			},
		},
		{
			name:    "up request succeeds for access replica",
			request: drbdconfig.UpRequest{testRVRUp},
			existingObjects: func() []client.Object {
				rvr := newRVR(testRVRUp, testRVUp, "Access",
					withRVRNode(testNodeName),
					withRVRStatus(minimalRVRStatus()))
				rv := rvWithSharedSecretAlg(testRVUp, "sha256")
				deviceMinor := uint(1)
				rv.Status.DRBD.Config.DeviceMinor = &deviceMinor
				return []client.Object{rvr, rv}
			}(),
			assertErr: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			},
		},
		{
			name:    "up request succeeds for diskful replica",
			request: drbdconfig.UpRequest{testRVRDiskful},
			existingObjects: func() []client.Object {
				rvr := newRVR(testRVRDiskful, testRVDiskful, "Diskful",
					withRVRNode(testNodeName),
					withRVRStatus(&v1alpha3.ReplicatedVolumeReplicaStatus{
						LVMLogicalVolumeName: testLlvDiskful,
						DRBD:                 minimalRVRStatus().DRBD,
					}))
				rv := rvWithSharedSecretAlg(testRVDiskful, "sha256")
				deviceMinor := uint(2)
				rv.Status.DRBD.Config.DeviceMinor = &deviceMinor

				llv := &sncv1alpha1.LVMLogicalVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       testLlvDiskful,
						Finalizers: []string{v1alpha3.AgentAppFinalizer},
					},
					Spec: sncv1alpha1.LVMLogicalVolumeSpec{
						ActualLVNameOnTheNode: testLlvDiskful,
						Type:                  "Thick",
						Size:                  "1Gi",
						LVMVolumeGroupName:    testLvgDiskful,
					},
				}

				lvg := &sncv1alpha1.LVMVolumeGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: testLvgDiskful,
					},
					Spec: sncv1alpha1.LVMVolumeGroupSpec{
						ActualVGNameOnTheNode: testLvgDiskful,
						Type:                  "Local",
						Local:                 sncv1alpha1.LVMVolumeGroupLocalSpec{NodeName: testNodeName},
					},
				}

				return []client.Object{rvr, rv, llv, lvg}
			}(),
			assertErr: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			},
		},
		{
			name:    "down request removes finalizers",
			request: drbdconfig.DownRequest{testRVRDown},
			existingObjects: func() []client.Object {
				// Diskful to ensure llv path is executed
				rvr := newRVR(testRVRDown, testRVDown, "Diskful",
					withRVRNode(testNodeName),
					withRVRFinalizers(v1alpha3.AgentAppFinalizer),
					withRVRStatus(&v1alpha3.ReplicatedVolumeReplicaStatus{
						LVMLogicalVolumeName: testLLVDown,
						DRBD:                 &v1alpha3.DRBD{},
					}))

				llv := &sncv1alpha1.LVMLogicalVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       testLLVDown,
						Finalizers: []string{v1alpha3.AgentAppFinalizer},
					},
				}
				return []client.Object{rvr, llv}
			}(),
			assertErr: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupMemFS(t)

			scheme, err := agentscheme.New()
			if err != nil {
				t.Fatalf("add scheme: %v", err)
			}

			builder := fake.NewClientBuilder().
				WithScheme(scheme)
			if _, ok := tc.request.(drbdconfig.RVRRequest); ok {
				builder = builder.WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}, &v1alpha3.ReplicatedVolume{}, &sncv1alpha1.LVMLogicalVolume{})
			}
			if len(tc.existingObjects) > 0 {
				builder = builder.WithObjects(tc.existingObjects...)
			}
			cl := builder.Build()

			// prepare filesystem for handlers
			if err := drbdconfig.FS.MkdirAll(drbdconfig.ResourcesDir, 0755); err != nil {
				t.Fatalf("prepare fs: %v", err)
			}
			if tc.fsSetup != nil {
				tc.fsSetup()
			}

			// mock drbdadm commands when needed
			if _, ok := tc.request.(drbdconfig.RVRRequest); ok {
				exec := drbdadmfake.NewExec(t)
				switch req := tc.request.(type) {
				case drbdconfig.UpRequest:
					rvrName := req.RVRRequestRVRName()
					switch rvrName {
					case testRVRDiskful:
						regular, tmp := drbdconfig.FilePaths(testRVDiskful)
						exec.ExpectCommand(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.DumpMDArgs(testRVDiskful), []byte("No valid meta data found"), drbdadmfake.ExitErr{Code: 1})
						exec.ExpectCommand(drbdadm.Command, drbdadm.CreateMDArgs(testRVDiskful), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.PrimaryForceArgs(testRVDiskful), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.SecondaryArgs(testRVDiskful), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.StatusArgs(testRVDiskful), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.AdjustArgs(testRVDiskful), nil, nil)
					default:
						regular, tmp := drbdconfig.FilePaths(testRVUp)
						exec.ExpectCommand(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.StatusArgs(testRVUp), nil, nil)
						exec.ExpectCommand(drbdadm.Command, drbdadm.AdjustArgs(testRVUp), nil, nil)
					}
				case drbdconfig.DownRequest:
					exec.ExpectCommand(drbdadm.Command, drbdadm.DownArgs(testRVDown), nil, nil)
				}
				exec.Setup()
			}

			rec := drbdconfig.NewReconciler(cl, cl, slog.Default(), testNodeName)

			_, err = rec.Reconcile(t.Context(), tc.request)
			tc.assertErr(t, err)

			switch req := tc.request.(type) {
			case drbdconfig.SharedSecretAlgRequest:
				rvr := &v1alpha3.ReplicatedVolumeReplica{}
				if getErr := cl.Get(t.Context(), client.ObjectKey{Name: testSharedRVRSSA}, rvr); getErr != nil {
					t.Fatalf("get rvr: %v", getErr)
				}
				hasErr := rvr.Status != nil && rvr.Status.DRBD != nil && rvr.Status.DRBD.Errors != nil &&
					rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil
				expectErr := tc.expectSharedSecretError != nil && *tc.expectSharedSecretError
				if hasErr != expectErr {
					t.Fatalf("unexpected SharedSecretAlgSelectionError presence, got %v want %v", hasErr, expectErr)
				}
			case drbdconfig.UpRequest:
				rvr := &v1alpha3.ReplicatedVolumeReplica{}
				if getErr := cl.Get(t.Context(), client.ObjectKey{Name: req.RVRRequestRVRName()}, rvr); getErr != nil {
					t.Fatalf("get rvr: %v", getErr)
				}
				if !containsFinalizer(rvr.Finalizers, v1alpha3.AgentAppFinalizer) ||
					!containsFinalizer(rvr.Finalizers, v1alpha3.ControllerAppFinalizer) {
					t.Fatalf("expected finalizers to include agent and controller, got %v", rvr.Finalizers)
				}
				if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Actual == nil ||
					!rvr.Status.DRBD.Actual.InitialSyncCompleted {
					t.Fatalf("expected initial sync completed in status")
				}
				var rvName string
				switch req.RVRRequestRVRName() {
				case testRVRDiskful:
					rvName = testRVDiskful
				default:
					rvName = testRVUp
				}
				regular, tmp := drbdconfig.FilePaths(rvName)
				if exists, _ := drbdconfig.FS.Exists(regular); !exists {
					t.Fatalf("expected config file %s to exist", regular)
				}
				if exists, _ := drbdconfig.FS.Exists(tmp); exists {
					t.Fatalf("expected temp config file %s to be removed", tmp)
				}
				if rvr.Spec.Type == "Diskful" {
					if rvr.Status.DRBD.Actual.Disk != "/dev/"+testLvgDiskful+"/"+testLlvDiskful {
						t.Fatalf("expected disk path set, got %s", rvr.Status.DRBD.Actual.Disk)
					}
					llv := &sncv1alpha1.LVMLogicalVolume{}
					if getErr := cl.Get(t.Context(), client.ObjectKey{Name: testLlvDiskful}, llv); getErr != nil {
						t.Fatalf("get llv: %v", getErr)
					}
					if !containsFinalizer(llv.Finalizers, v1alpha3.AgentAppFinalizer) {
						t.Fatalf("expected llv finalizer to include agent, got %v", llv.Finalizers)
					}
				}
			case drbdconfig.DownRequest:
				rvr := &v1alpha3.ReplicatedVolumeReplica{}
				if getErr := cl.Get(t.Context(), client.ObjectKey{Name: req.RVRRequestRVRName()}, rvr); getErr != nil {
					t.Fatalf("get rvr: %v", getErr)
				}
				if len(rvr.Finalizers) != 0 {
					t.Fatalf("expected rvr finalizers to be removed, got %v", rvr.Finalizers)
				}

				llv := &sncv1alpha1.LVMLogicalVolume{}
				if getErr := cl.Get(t.Context(), client.ObjectKey{Name: testLLVDown}, llv); getErr != nil {
					t.Fatalf("get llv: %v", getErr)
				}
				if len(llv.Finalizers) != 0 {
					t.Fatalf("expected llv finalizers to be removed, got %v", llv.Finalizers)
				}
			}
		})
	}
}

// ---- helpers ----

func rvWithSharedSecretAlg(name, alg string) *v1alpha3.ReplicatedVolume {
	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{
					SharedSecret:    "secret",
					SharedSecretAlg: alg,
					DeviceMinor:     u.Ptr(uint(0)),
				},
			},
		},
	}
	return rv
}

type rvrOption func(*v1alpha3.ReplicatedVolumeReplica)

func newRVR(name, rvName, rvrType string, opts ...rvrOption) *v1alpha3.ReplicatedVolumeReplica {
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			Type:                 rvrType,
		},
	}
	for _, opt := range opts {
		opt(rvr)
	}
	return rvr
}

func withRVRNode(node string) rvrOption {
	return func(rvr *v1alpha3.ReplicatedVolumeReplica) {
		rvr.Spec.NodeName = node
	}
}

func withRVRDeletion() rvrOption {
	return func(rvr *v1alpha3.ReplicatedVolumeReplica) {
		now := metav1.NewTime(time.Now())
		rvr.DeletionTimestamp = &now
	}
}

func withRVRFinalizers(finalizers ...string) rvrOption {
	return func(rvr *v1alpha3.ReplicatedVolumeReplica) {
		rvr.Finalizers = append([]string{}, finalizers...)
	}
}

func withRVRStatus(status *v1alpha3.ReplicatedVolumeReplicaStatus) rvrOption {
	return func(rvr *v1alpha3.ReplicatedVolumeReplica) {
		rvr.Status = status
	}
}

func minimalRVRStatus() *v1alpha3.ReplicatedVolumeReplicaStatus {
	nodeID := uint(1)
	return &v1alpha3.ReplicatedVolumeReplicaStatus{
		DRBD: &v1alpha3.DRBD{
			Config: &v1alpha3.DRBDConfig{
				NodeId:           &nodeID,
				Address:          &v1alpha3.Address{IPv4: "10.0.0.1", Port: 7000},
				PeersInitialized: true,
				Peers:            map[string]v1alpha3.Peer{},
			},
			Actual: &v1alpha3.DRBDActual{},
		},
	}
}

func containsFinalizer(finalizers []string, target string) bool {
	for _, f := range finalizers {
		if f == target {
			return true
		}
	}
	return false
}
