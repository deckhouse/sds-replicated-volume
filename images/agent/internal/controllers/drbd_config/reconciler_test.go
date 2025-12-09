package drbdconfig_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	drbdconfig "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd_config"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha3.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha3 scheme: %v", err)
	}
	if err := snc.AddToScheme(scheme); err != nil {
		t.Fatalf("add snc scheme: %v", err)
	}
	return scheme
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func withTestFS(t *testing.T) (afero.Fs, func()) {
	t.Helper()
	mem := afero.NewMemMapFs()
	restore := drbdconfig.SetFSForTests(mem)
	return mem, restore
}

type fakeCmd struct {
	runErr    error
	output    []byte
	stderr    io.Writer
	outputErr error
}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return e.code }

func (c *fakeCmd) CombinedOutput() ([]byte, error) { return c.output, c.outputErr }
func (c *fakeCmd) SetStderr(w io.Writer)           { c.stderr = w }
func (c *fakeCmd) Run() error {
	if c.runErr != nil && c.stderr != nil && len(c.output) > 0 {
		_, _ = c.stderr.Write(c.output)
	}
	return c.runErr
}

func stubExecCmd(responses map[string]fakeCmd) func(ctx context.Context, name string, arg ...string) drbdadm.Cmd {
	return func(ctx context.Context, name string, arg ...string) drbdadm.Cmd {
		key := strings.Join(arg, " ")
		if cmd, ok := responses[key]; ok {
			return &cmd
		}
		return &fakeCmd{}
	}
}

type fakeQueue struct {
	items    []drbdconfig.TReq
	shutdown bool
}

func (q *fakeQueue) Add(item drbdconfig.TReq)                       { q.items = append(q.items, item) }
func (q *fakeQueue) AddAfter(item drbdconfig.TReq, _ time.Duration) { q.Add(item) }
func (q *fakeQueue) AddRateLimited(item drbdconfig.TReq)            { q.Add(item) }
func (q *fakeQueue) Done(drbdconfig.TReq)                           {}
func (q *fakeQueue) Forget(drbdconfig.TReq)                         {}
func (q *fakeQueue) Get() (item drbdconfig.TReq, shutdown bool) {
	if q.shutdown {
		return item, true
	}
	if len(q.items) == 0 {
		return item, false
	}
	item = q.items[0]
	q.items = q.items[1:]
	return item, false
}
func (q *fakeQueue) Len() int                        { return len(q.items) }
func (q *fakeQueue) NumRequeues(drbdconfig.TReq) int { return 0 }
func (q *fakeQueue) ShutDown()                       { q.shutdown = true }
func (q *fakeQueue) ShuttingDown() bool              { return q.shutdown }
func (q *fakeQueue) ShutDownWithDrain()              { q.shutdown = true }

func toRuntimeObjects(objs []ctrlclient.Object) []runtime.Object {
	res := make([]runtime.Object, 0, len(objs))
	for _, obj := range objs {
		res = append(res, obj)
	}
	return res
}

func TestReconciler_OnRVUpdate(t *testing.T) {
	logger := newTestLogger()

	tests := []struct {
		name           string
		oldRV          *v1alpha3.ReplicatedVolume
		newRV          *v1alpha3.ReplicatedVolume
		wantQueueItems []drbdconfig.Request
	}{
		{
			name:  "ignore-when-new-status-missing",
			oldRV: &v1alpha3.ReplicatedVolume{},
			newRV: &v1alpha3.ReplicatedVolume{},
		},
		{
			name:  "enqueue-when-shared-secret-alg-changed",
			oldRV: &v1alpha3.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv1"}},
			newRV: &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv1"},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{
							SharedSecretAlg: "hmac-sha256",
						},
					},
				},
			},
			wantQueueItems: []drbdconfig.Request{
				drbdconfig.NewSharedSecretAlgRequest("rv1", "hmac-sha256"),
			},
		},
		{
			name: "ignore-when-alg-unchanged",
			oldRV: &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv1"},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{SharedSecretAlg: "hmac-sha256"},
					},
				},
			},
			newRV: &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv1"},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{SharedSecretAlg: "hmac-sha256"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQueue{}
			rec := drbdconfig.NewReconciler(nil, nil, logger, "")

			rec.OnRVUpdate(context.Background(), tt.oldRV, tt.newRV, q)

			if got := q.Len(); got != len(tt.wantQueueItems) {
				t.Fatalf("queue len: got %d, want %d", got, len(tt.wantQueueItems))
			}
			for _, want := range tt.wantQueueItems {
				item, _ := q.Get()
				req, ok := item.(drbdconfig.SharedSecretAlgRequest)
				if !ok {
					t.Fatalf("unexpected queue item type %T", item)
				}
				if req != want.(drbdconfig.SharedSecretAlgRequest) {
					t.Fatalf("queue item mismatch: got %#v want %#v", req, want)
				}
			}
		})
	}
}

func TestReconciler_OnRVRCreateOrUpdate(t *testing.T) {
	logger := newTestLogger()
	scheme := testScheme(t)

	rvReady := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv1"},
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{
					SharedSecret:            "secret",
					SharedSecretAlg:         "hmac-sha256",
					DeviceMinor:             ptrTo[uint](1),
					AllowTwoPrimaries:       false,
					Quorum:                  1,
					QuorumMinimumRedundancy: 1,
				},
			},
		},
	}

	tests := []struct {
		name           string
		clientObjs     []ctrlclient.Object
		rvr            *v1alpha3.ReplicatedVolumeReplica
		wantQueueItems []drbdconfig.Request
	}{
		{
			name:       "ignore-when-rvr-not-on-node",
			clientObjs: nil,
			rvr: &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
				Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "other"},
			},
		},
		{
			name:       "ignore-when-non-agent-finalizer-present",
			clientObjs: nil,
			rvr: &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rvr1",
					DeletionTimestamp: ptrTo(metav1.NewTime(time.Now())),
					Finalizers:        []string{"custom"},
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node1"},
			},
		},
		{
			name:       "enqueue-down-on-delete",
			clientObjs: nil,
			rvr: &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rvr1",
					DeletionTimestamp: ptrTo(metav1.NewTime(time.Now())),
					Finalizers:        []string{v1alpha3.AgentAppFinalizer},
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node1",
				},
			},
			wantQueueItems: []drbdconfig.Request{drbdconfig.NewDownRequest("rvr1")},
		},
		{
			name:       "ignore-when-rv-fetch-fails",
			clientObjs: nil,
			rvr: &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName:             "node1",
					ReplicatedVolumeName: "rv-missing",
					Type:                 "Diskful",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Config: &v1alpha3.DRBDConfig{
							NodeId:           ptrTo[uint](1),
							Address:          &v1alpha3.Address{IPv4: "10.0.0.1", Port: 1234},
							PeersInitialized: true,
							Peers:            map[string]v1alpha3.Peer{},
						},
					},
				},
			},
		},
		{
			name:       "ignore-when-not-initialized",
			clientObjs: []ctrlclient.Object{rvReady},
			rvr: &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName:             "node1",
					ReplicatedVolumeName: rvReady.Name,
					Type:                 "Access",
				},
			},
		},
		{
			name:       "enqueue-up-when-ready",
			clientObjs: []ctrlclient.Object{rvReady},
			rvr: &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName:             "node1",
					ReplicatedVolumeName: rvReady.Name,
					Type:                 "Access",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Config: &v1alpha3.DRBDConfig{
							NodeId:           ptrTo[uint](1),
							Address:          &v1alpha3.Address{IPv4: "10.0.0.1", Port: 1234},
							PeersInitialized: true,
							Peers:            map[string]v1alpha3.Peer{},
						},
					},
				},
			},
			wantQueueItems: []drbdconfig.Request{drbdconfig.NewUpRequest("rvr1")},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQueue{}
			c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(toRuntimeObjects(tt.clientObjs)...).Build()
			rec := drbdconfig.NewReconciler(c, c, logger, "node1")

			rec.OnRVRCreateOrUpdate(context.Background(), tt.rvr.DeepCopy(), q)

			if got := q.Len(); got != len(tt.wantQueueItems) {
				t.Fatalf("queue len: got %d, want %d", got, len(tt.wantQueueItems))
			}
			for _, want := range tt.wantQueueItems {
				item, _ := q.Get()
				if _, ok := item.(drbdconfig.RVRRequest); !ok {
					t.Fatalf("expected RVRRequest, got %T", item)
				}
				if item != want {
					t.Fatalf("queue item mismatch: got %#v want %#v", item, want)
				}
			}
		})
	}
}

func TestReconciler_Reconcile(t *testing.T) {
	logger := newTestLogger()
	scheme := testScheme(t)

	tests := []struct {
		name    string
		req     drbdconfig.Request
		objects []ctrlclient.Object
		wantErr bool
	}{
		{
			name:    "rvr-request-not-found",
			req:     drbdconfig.NewUpRequest("missing"),
			objects: nil,
			wantErr: false,
		},
		{
			name: "shared-secret-checked",
			req:  drbdconfig.NewSharedSecretAlgRequest("rv1", "hmac(sha256)"),
			objects: []ctrlclient.Object{
				&v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						NodeName:             "node1",
						ReplicatedVolumeName: "rv1",
					},
					Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha3.DRBD{
							Errors: &v1alpha3.DRBDErrors{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "unknown-request",
			req:     drbdconfig.NewUnknownRequestForTests(),
			objects: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(toRuntimeObjects(tt.objects)...).Build()
			rec := drbdconfig.NewReconciler(cl, cl, logger, "node1")

			_, err := rec.Reconcile(context.Background(), tt.req)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestReconciler_reconcileRVRRequest(t *testing.T) {
	logger := newTestLogger()
	scheme := testScheme(t)

	memFS, restoreFS := withTestFS(t)
	defer restoreFS()
	afs := afero.Afero{Fs: memFS}
	if err := afs.MkdirAll("/drbd", 0755); err != nil {
		t.Fatalf("mkdir /drbd: %v", err)
	}

	oldResourcesDir := drbdconfig.ResourcesDir
	drbdconfig.ResourcesDir = "/drbd/"
	defer func() { drbdconfig.ResourcesDir = oldResourcesDir }()

	ctx := context.Background()

	baseRV := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv1"},
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{
					SharedSecret:            "secret",
					SharedSecretAlg:         "hmac-sha256",
					AllowTwoPrimaries:       false,
					Quorum:                  1,
					QuorumMinimumRedundancy: 1,
					DeviceMinor:             ptrTo[uint](1),
				},
			},
		},
	}

	baseRVR := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			NodeName:             "node1",
			ReplicatedVolumeName: baseRV.Name,
			Type:                 "Access",
		},
		Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
			DRBD: &v1alpha3.DRBD{
				Config: &v1alpha3.DRBDConfig{
					NodeId:           ptrTo[uint](1),
					Address:          &v1alpha3.Address{IPv4: "10.0.0.1", Port: 1234},
					PeersInitialized: true,
					Peers:            map[string]v1alpha3.Peer{},
				},
			},
		},
	}

	prepareClient := func(objs ...ctrlclient.Object) ctrlclient.Client {
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
			WithRuntimeObjects(toRuntimeObjects(objs)...).
			Build()
	}

	tests := []struct {
		name       string
		req        drbdconfig.Request
		clientObjs []ctrlclient.Object
		setupExec  func()
		prepareFS  func()
		check      func(t *testing.T, cl ctrlclient.Client)
		expectErr  bool
	}{
		{
			name:       "rvr-not-found",
			req:        drbdconfig.NewUpRequest("absent"),
			clientObjs: nil,
		},
		{
			name: "rvr-on-other-node",
			req:  drbdconfig.NewUpRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				func() *v1alpha3.ReplicatedVolumeReplica {
					r := baseRVR.DeepCopy()
					r.Spec.NodeName = "other"
					return r
				}(),
			},
		},
		{
			name:       "unknown-rvr-request",
			req:        drbdconfig.NewUnknownRVRRequestForTests("rvr1"),
			clientObjs: []ctrlclient.Object{baseRVR.DeepCopy()},
			expectErr:  true,
		},
		{
			name: "up-success",
			req:  drbdconfig.NewUpRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				baseRV.DeepCopy(),
				baseRVR.DeepCopy(),
			},
			setupExec: func() {
				drbdadm.ExecCommandContext = stubExecCmd(nil)
			},
			check: func(t *testing.T, cl ctrlclient.Client) {
				exists, _ := afs.Exists("/drbd/rv1.res")
				if !exists {
					t.Fatalf("expected config file to be created")
				}
				rvr := &v1alpha3.ReplicatedVolumeReplica{}
				if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: "rvr1"}, rvr); err != nil {
					t.Fatalf("get rvr: %v", err)
				}
				if !contains(rvr.Finalizers, v1alpha3.AgentAppFinalizer) || !contains(rvr.Finalizers, v1alpha3.ControllerAppFinalizer) {
					t.Fatalf("finalizers not patched: %v", rvr.Finalizers)
				}
				if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Actual == nil || !rvr.Status.DRBD.Actual.InitialSyncCompleted {
					t.Fatalf("status not updated: %#v", rvr.Status)
				}
			},
		},
		{
			name: "up-drbd-adjust-failure",
			req:  drbdconfig.NewUpRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				baseRV.DeepCopy(),
				baseRVR.DeepCopy(),
			},
			setupExec: func() {
				drbdadm.ExecCommandContext = stubExecCmd(map[string]fakeCmd{
					"adjust rv1": {outputErr: errors.New("adjust failed")},
				})
			},
			expectErr: true,
		},
		{
			name: "down-non-agent-finalizer",
			req:  drbdconfig.NewDownRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				func() *v1alpha3.ReplicatedVolumeReplica {
					r := baseRVR.DeepCopy()
					r.Finalizers = []string{"custom"}
					return r
				}(),
			},
		},
		{
			name: "down-success",
			req:  drbdconfig.NewDownRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				func() *v1alpha3.ReplicatedVolumeReplica {
					r := baseRVR.DeepCopy()
					r.Spec.Type = "Diskful"
					r.Status.LVMLogicalVolumeName = "llv1"
					r.Finalizers = []string{v1alpha3.AgentAppFinalizer}
					return r
				}(),
				&snc.LVMLogicalVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "llv1", Finalizers: []string{v1alpha3.AgentAppFinalizer}},
				},
			},
			setupExec: func() {
				drbdadm.ExecCommandContext = stubExecCmd(nil)
			},
			prepareFS: func() {
				_ = afs.WriteFile("/drbd/rv1.res", []byte("config"), 0644)
				_ = afs.WriteFile("/drbd/rv1.res_tmp", []byte("tmp"), 0644)
			},
			check: func(t *testing.T, cl ctrlclient.Client) {
				rvr := &v1alpha3.ReplicatedVolumeReplica{}
				if err := cl.Get(ctx, ctrlclient.ObjectKey{Name: "rvr1"}, rvr); err != nil {
					t.Fatalf("get rvr: %v", err)
				}
				if len(rvr.Finalizers) != 0 {
					t.Fatalf("expected rvr finalizers removed, got %v", rvr.Finalizers)
				}
			},
		},
		{
			name: "down-exec-error",
			req:  drbdconfig.NewDownRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				func() *v1alpha3.ReplicatedVolumeReplica {
					r := baseRVR.DeepCopy()
					r.Spec.Type = "Diskful"
					r.Status.LVMLogicalVolumeName = "llv2"
					r.Finalizers = []string{v1alpha3.AgentAppFinalizer}
					return r
				}(),
				&snc.LVMLogicalVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "llv2", Finalizers: []string{v1alpha3.AgentAppFinalizer}},
				},
			},
			setupExec: func() {
				drbdadm.ExecCommandContext = stubExecCmd(map[string]fakeCmd{
					"down rv1": {outputErr: errors.New("down failed")},
				})
			},
		},
		{
			name: "up-diskful-initial-sync",
			req:  drbdconfig.NewUpRequest("rvr1"),
			clientObjs: []ctrlclient.Object{
				baseRV.DeepCopy(),
				func() *v1alpha3.ReplicatedVolumeReplica {
					r := baseRVR.DeepCopy()
					r.Spec.Type = "Diskful"
					r.Status.LVMLogicalVolumeName = "llv1"
					r.Status.DRBD.Status = &v1alpha3.DRBDStatus{}
					r.Status.DRBD.Actual = &v1alpha3.DRBDActual{}
					return r
				}(),
				&snc.LVMLogicalVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "llv1"},
					Spec: snc.LVMLogicalVolumeSpec{
						LVMVolumeGroupName:    "lvg1",
						ActualLVNameOnTheNode: "lvnode",
					},
				},
				&snc.LVMVolumeGroup{
					ObjectMeta: metav1.ObjectMeta{Name: "lvg1"},
					Spec: snc.LVMVolumeGroupSpec{
						ActualVGNameOnTheNode: "vgnode",
					},
				},
			},
			setupExec: func() {
				drbdadm.ExecCommandContext = stubExecCmd(map[string]fakeCmd{
					"dump-md --force rv1": {runErr: exitErr{code: 1}, output: []byte("No valid meta data found")},
				})
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			origExec := drbdadm.ExecCommandContext
			drbdadm.ExecCommandContext = stubExecCmd(nil)
			if tt.setupExec != nil {
				tt.setupExec()
			}
			defer func() { drbdadm.ExecCommandContext = origExec }()
			if tt.prepareFS != nil {
				tt.prepareFS()
			}

			cl := prepareClient(tt.clientObjs...)
			rec := drbdconfig.NewReconciler(cl, cl, logger, "node1")

			_, err := rec.Reconcile(ctx, tt.req)
			if tt.expectErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.check != nil {
				tt.check(t, cl)
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func TestHelpers_rvrOnThisNodeAndInit(t *testing.T) {
	logger := newTestLogger()
	rec := drbdconfig.NewReconciler(nil, nil, logger, "node1")

	rv := &v1alpha3.ReplicatedVolume{
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{
					SharedSecret:    "secret",
					SharedSecretAlg: "sha256",
				},
			},
		},
	}

	baseRVR := &v1alpha3.ReplicatedVolumeReplica{
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: "rv1",
			Type:                 "Diskless",
			NodeName:             "node1",
		},
		Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
			DRBD: &v1alpha3.DRBD{
				Config: &v1alpha3.DRBDConfig{
					NodeId:           ptrTo[uint](1),
					Address:          &v1alpha3.Address{IPv4: "10.0.0.1", Port: 1234},
					PeersInitialized: true,
					Peers:            map[string]v1alpha3.Peer{},
				},
			},
		},
	}

	cases := []struct {
		name string
		rvr  *v1alpha3.ReplicatedVolumeReplica
		rv   *v1alpha3.ReplicatedVolume
		want bool
	}{
		{"on-node", baseRVR.DeepCopy(), rv, true},
		{"missing-name", &v1alpha3.ReplicatedVolumeReplica{}, rv, false},
		{"missing-rv-status", baseRVR.DeepCopy(), &v1alpha3.ReplicatedVolume{}, false},
		{
			name: "missing-nodeid",
			rvr: func() *v1alpha3.ReplicatedVolumeReplica {
				r := baseRVR.DeepCopy()
				r.Status.DRBD.Config.NodeId = nil
				return r
			}(),
			rv:   rv,
			want: false,
		},
		{
			name: "missing-address",
			rvr: func() *v1alpha3.ReplicatedVolumeReplica {
				r := baseRVR.DeepCopy()
				r.Status.DRBD.Config.Address = nil
				return r
			}(),
			rv:   rv,
			want: false,
		},
		{
			name: "peers-not-initialized",
			rvr: func() *v1alpha3.ReplicatedVolumeReplica {
				r := baseRVR.DeepCopy()
				r.Status.DRBD.Config.PeersInitialized = false
				return r
			}(),
			rv:   rv,
			want: false,
		},
		{
			name: "diskful-missing-llv",
			rvr: func() *v1alpha3.ReplicatedVolumeReplica {
				r := baseRVR.DeepCopy()
				r.Spec.Type = "Diskful"
				return r
			}(),
			rv:   rv,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := drbdconfig.ExposeRvrOnThisNode(rec, tc.rvr); got != (tc.rvr.Spec.NodeName == "node1" && tc.rvr.Spec.NodeName != "") {
				t.Fatalf("unexpected rvrOnThisNode result")
			}
			if got := drbdconfig.ExposeRvrInitialized(rec, tc.rvr, tc.rv); got != tc.want {
				t.Fatalf("init result got %v want %v", got, tc.want)
			}
		})
	}
}

func TestHelper_sharedSecretAlgUpdated(t *testing.T) {
	rec := drbdconfig.NewReconciler(nil, nil, newTestLogger(), "node1")

	rv := &v1alpha3.ReplicatedVolume{
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{SharedSecretAlg: "hmac-sha256"},
			},
		},
	}

	if !drbdconfig.ExposeSharedSecretAlgUpdated(rec, rv, nil, nil) {
		t.Fatalf("expected shared secret alg to be considered updated")
	}
}

func TestHelper_kernelHasCrypto(t *testing.T) {
	mem, restore := withTestFS(t)
	defer restore()
	afs := afero.Afero{Fs: mem}

	if err := afs.MkdirAll("/proc", 0755); err != nil {
		t.Fatalf("mkdir /proc: %v", err)
	}
	if err := afs.WriteFile("/proc/crypto", []byte("name         : aes\n\n"), 0644); err != nil {
		t.Fatalf("write crypto: %v", err)
	}

	ok, err := drbdconfig.ExposeKernelHasCrypto("aes")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatalf("expected algorithm present")
	}

	ok, err = drbdconfig.ExposeKernelHasCrypto("missing")
	if err != nil {
		t.Fatalf("unexpected error for missing algorithm: %v", err)
	}
	if ok {
		t.Fatalf("expected algorithm to be missing")
	}
}

func TestSharedSecretAlgHandlerUnsupported(t *testing.T) {
	mem, restore := withTestFS(t)
	defer restore()
	afs := afero.Afero{Fs: mem}
	if err := afs.MkdirAll("/proc", 0755); err != nil {
		t.Fatalf("mkdir /proc: %v", err)
	}
	if err := afs.WriteFile("/proc/crypto", []byte("name         : aes\n\n"), 0644); err != nil {
		t.Fatalf("write crypto: %v", err)
	}

	scheme := testScheme(t)
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			NodeName:             "node1",
			ReplicatedVolumeName: "rv1",
		},
		Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
			DRBD: &v1alpha3.DRBD{
				Errors: &v1alpha3.DRBDErrors{},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rvr).Build()
	rec := drbdconfig.NewReconciler(cl, cl, newTestLogger(), "node1")

	_, err := rec.Reconcile(context.Background(), drbdconfig.NewSharedSecretAlgRequest("rv1", "hmac-sha256"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(context.Background(), ctrlclient.ObjectKey{Name: "rvr1"}, updated); err != nil {
		t.Fatalf("get rvr: %v", err)
	}
	if updated.Status.DRBD.Errors.SharedSecretAlgSelectionError == nil {
		t.Fatalf("expected shared secret alg error set")
	}
}

type fakeCmdError struct {
	cmd []string
	out string
}

func (f fakeCmdError) Error() string             { return "cmdErr" }
func (f fakeCmdError) CommandWithArgs() []string { return f.cmd }
func (f fakeCmdError) Output() string            { return f.out }
func (f fakeCmdError) ExitCode() int             { return 5 }

func TestHelper_trimAndErrors(t *testing.T) {
	long := strings.Repeat("a", 2000)
	if got := drbdconfig.ExposeTrimLen(long, 10); len(got) != 10 {
		t.Fatalf("expected trim to 10, got %d", len(got))
	}

	msgErr := drbdconfig.ExposeFileSystemError(errors.New("fs failed"))
	if msgErr == nil || !strings.Contains(msgErr.Message, "fs failed") {
		t.Fatalf("expected filesystem error, got %#v", msgErr)
	}

	cmdErr := drbdconfig.ExposeCommandError(fakeCmdError{cmd: []string{"drbdadm", "up"}, out: "boom"})
	if cmdErr == nil || cmdErr.ExitCode != 5 || !strings.Contains(cmdErr.Command, "drbdadm up") {
		t.Fatalf("unexpected cmd error %#v", cmdErr)
	}
}

func TestHelper_APIAddressAndGenerateResource(t *testing.T) {
	rv := &v1alpha3.ReplicatedVolume{
		Status: &v1alpha3.ReplicatedVolumeStatus{
			DRBD: &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{
					SharedSecret:            "secret",
					SharedSecretAlg:         "sha256",
					AllowTwoPrimaries:       false,
					Quorum:                  0,
					QuorumMinimumRedundancy: 0,
					DeviceMinor:             ptrTo[uint](1),
				},
			},
		},
	}
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: "rv1",
			NodeName:             "node1",
			Type:                 "Diskful",
		},
		Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
			LVMLogicalVolumeName: "llv1",
			DRBD: &v1alpha3.DRBD{
				Config: &v1alpha3.DRBDConfig{
					NodeId:           ptrTo[uint](1),
					Address:          &v1alpha3.Address{IPv4: "10.0.0.1", Port: 7789},
					PeersInitialized: true,
					Peers: map[string]v1alpha3.Peer{
						"node2": {NodeId: 2, Address: v1alpha3.Address{IPv4: "10.0.0.2", Port: 7789}, Diskless: true},
					},
				},
			},
		},
	}
	lvg := &snc.LVMVolumeGroup{Spec: snc.LVMVolumeGroupSpec{ActualVGNameOnTheNode: "vg1"}}
	llv := &snc.LVMLogicalVolume{Spec: snc.LVMLogicalVolumeSpec{ActualLVNameOnTheNode: "lv1"}}

	h := drbdconfig.NewUpHandlerForTests(rvr, rv, lvg, llv, "node1")
	res := drbdconfig.ExposeGenerateResourceConfig(h)
	if len(res.On) != 2 || len(res.Connections) != 1 {
		t.Fatalf("expected resource to have local+peer definitions, got %#v", res)
	}

	addr := drbdconfig.ExposeAPIAddress("nodeX", v1alpha3.Address{IPv4: "1.2.3.4", Port: 9999})
	if addr.Name != "nodeX" || !strings.Contains(addr.AddressWithPort, "1.2.3.4:9999") {
		t.Fatalf("unexpected address %#v", addr)
	}
}

func TestHelper_RequestMarkersAndNewReconciler(t *testing.T) {
	drbdconfig.CallIsRequestMarkers()
	if rec := drbdconfig.NewReconciler(nil, nil, nil, "nodeX"); rec == nil {
		t.Fatalf("expected reconciler constructed")
	}
}
