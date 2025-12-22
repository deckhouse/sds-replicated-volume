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

package drbdconfig_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	drbdconfig "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd_config"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	fakedrbdadm "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm/fake"
)

type reconcileTestCase struct {
	name string
	//
	rv   *v1alpha1.ReplicatedVolume
	rvr  *v1alpha1.ReplicatedVolumeReplica
	llv  *snc.LVMLogicalVolume
	lvg  *snc.LVMVolumeGroup
	objs []client.Object
	//
	needsResourcesDir    bool
	cryptoAlgs           []string
	expectedReconcileErr error
	expectedCommands     []*fakedrbdadm.ExpectedCmd
	prepare              func(t *testing.T)
	postCheck            func(t *testing.T, cl client.Client)
}

const (
	testRVName              = "testRVName"
	testNodeName            = "testNodeName"
	testPeerNodeName        = "peer-node"
	testRVRName             = "test-rvr"
	testRVRAltName          = "test-rvr-alt"
	testRVRDeleteName       = "test-rvr-delete"
	testRVSecret            = "secret"
	testAlgSHA256           = "sha256"
	testAlgUnsupported      = "sha512"
	testPeerIPv4            = "10.0.0.2"
	testNodeIPv4            = "10.0.0.1"
	testPortBase       uint = 7000
	testLVGName             = "test-vg"
	testLLVName             = "test-llv"
	testDiskName            = "test-lv"
	rvrTypeDiskful          = "Diskful"
	rvrTypeAccess           = "Access"
	testNodeIDLocal         = 0
	testPeerNodeID          = 1
	apiGroupStorage         = "storage.deckhouse.io"
	resourceLLV             = "lvmlogicalvolumes"
	resourceLVG             = "lvmvolumegroups"
)

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

func TestReconciler_Reconcile(t *testing.T) {
	testCases := []*reconcileTestCase{
		{
			name: "empty cluster",
			rv:   testRV(),
		},
		{
			name: "rvr not initialized",
			rv:   testRV(),
			rvr:  rvrSpecOnly("rvr-not-initialized", rvrTypeDiskful),
		},
		{
			name: "rvr missing status fields skips work",
			rv:   testRV(),
			rvr:  disklessRVR(testRVRName, addr(testNodeIPv4, port(0))),
		},
		{
			name: "rv missing shared secret skips work",
			rv:   rvWithoutSecret(),
			rvr:  disklessRVR(testRVRName, addr(testNodeIPv4, port(0))),
		},
		{
			name: "duplicate rvr on node fails selection",
			rv:   testRV(),
			rvr:  disklessRVR(testRVRName, addr(testNodeIPv4, port(0))),
			objs: []client.Object{
				disklessRVR("test-rvr-dup", addr(testNodeIPv4, port(1))),
			},
			expectedReconcileErr: errors.New("selecting rvr: more then one rvr exists"),
		},
		{
			name:                 "diskful llv missing returns error",
			rv:                   readyRVWithConfig(testRVSecret, testAlgSHA256, 1, false),
			rvr:                  diskfulRVR(testRVRAltName, addr(testNodeIPv4, port(100)), testLLVName),
			needsResourcesDir:    true,
			cryptoAlgs:           []string{testAlgSHA256},
			expectedReconcileErr: selectErr("llv", resourceLLV, testLLVName),
		},
		{
			name:                 "diskful lvg missing returns error",
			rv:                   readyRVWithConfig(testRVSecret, testAlgSHA256, 2, true),
			rvr:                  diskfulRVR(testRVRAltName, addr(testNodeIPv4, port(101)), testLLVName),
			llv:                  newLLV(testLLVName, testLVGName, testDiskName),
			needsResourcesDir:    true,
			cryptoAlgs:           []string{testAlgSHA256},
			expectedReconcileErr: selectErr("lvg", resourceLVG, testLVGName),
		},
		{
			name: "deleting diskful rvr cleans up",
			rv:   readyRVWithConfig(testRVSecret, testAlgSHA256, 1, false),
			rvr:  deletingRVR(testRVRDeleteName, testLLVName),
			llv:  newLLV(testLLVName, testLVGName, testDiskName),
			expectedCommands: []*fakedrbdadm.ExpectedCmd{
				newExpectedCmd(drbdadm.Command, drbdadm.DownArgs(testRVName), "", nil),
			},
			prepare: func(t *testing.T) {
				regular, tmp := drbdconfig.FilePaths(testRVName)
				mustWriteFile(t, regular, []byte("data"))
				mustWriteFile(t, tmp, []byte("data"))
			},
			postCheck: func(t *testing.T, cl client.Client) {
				if rvr, err := tryGetRVR(t, cl, testRVRDeleteName); err == nil {
					expectFinalizers(t, rvr.Finalizers)
				} else if !apierrors.IsNotFound(err) {
					t.Fatalf("getting rvr after reconcile: %v", err)
				}

				if llv, err := tryGetLLV(t, cl, testLLVName); err == nil {
					expectFinalizers(t, llv.Finalizers)
				} else if !apierrors.IsNotFound(err) {
					t.Fatalf("getting llv after reconcile: %v", err)
				}
				regular, tmp := drbdconfig.FilePaths(testRVName)
				expectFileAbsent(t, regular, tmp)
			},
		},
		{
			name:              "diskless rvr adjusts config",
			rv:                readyRVWithConfig(testRVSecret, testAlgSHA256, 1, false),
			rvr:               disklessRVR(testRVRName, addr(testNodeIPv4, port(0)), peersFrom(peerDisklessSpec(testPeerNodeName, testPeerNodeID, addr(testPeerIPv4, port(1))))),
			needsResourcesDir: true,
			cryptoAlgs:        []string{testAlgSHA256},
			expectedCommands:  disklessExpectedCommands(testRVName),
			postCheck: func(t *testing.T, cl client.Client) {
				rvr := fetchRVR(t, cl, testRVRName)
				expectFinalizers(t, rvr.Finalizers, v1alpha1.AgentAppFinalizer, v1alpha1.ControllerAppFinalizer)
				expectTrue(t, rvr.Status.DRBD.Actual.InitialSyncCompleted, "initial sync completed")
				expectNoDRBDErrors(t, rvr.Status.DRBD.Errors)
			},
		},
		{
			name:              "drbd errors are reset after successful reconcile",
			rv:                readyRVWithConfig(testRVSecret, testAlgSHA256, 1, false),
			rvr:               rvrWithErrors(disklessRVR(testRVRAltName, addr(testNodeIPv4, port(2)), peersFrom(peerDisklessSpec(testPeerNodeName, testPeerNodeID, addr(testPeerIPv4, port(4)))))),
			needsResourcesDir: true,
			cryptoAlgs:        []string{testAlgSHA256},
			expectedCommands:  disklessExpectedCommands(testRVName),
			postCheck: func(t *testing.T, cl client.Client) {
				rvr := fetchRVR(t, cl, testRVRAltName)
				expectNoDRBDErrors(t, rvr.Status.DRBD.Errors)
			},
		},
		{
			name:              "diskful rvr creates metadata and adjusts",
			rv:                readyRVWithConfig(testRVSecret, testAlgSHA256, 2, true),
			rvr:               diskfulRVR(testRVRAltName, addr(testNodeIPv4, port(100)), testLLVName),
			llv:               newLLV(testLLVName, testLVGName, testDiskName),
			lvg:               newLVG(testLVGName),
			needsResourcesDir: true,
			cryptoAlgs:        []string{testAlgSHA256},
			expectedCommands:  diskfulExpectedCommands(testRVName),
			postCheck: func(t *testing.T, cl client.Client) {
				rvr := fetchRVR(t, cl, testRVRAltName)
				expectFinalizers(t, rvr.Finalizers, v1alpha1.AgentAppFinalizer, v1alpha1.ControllerAppFinalizer)
				expectString(t, rvr.Status.DRBD.Actual.Disk, "/dev/"+testLVGName+"/"+testDiskName, "actual disk")
				expectTrue(t, rvr.Status.DRBD.Actual.InitialSyncCompleted, "initial sync completed")
			},
		},
		{
			name:                 "sh-nop failure bubbles up",
			rv:                   readyRVWithConfig(testRVSecret, testAlgSHA256, 3, false),
			rvr:                  disklessRVR(testRVRName, addr(testNodeIPv4, port(10))),
			needsResourcesDir:    true,
			cryptoAlgs:           []string{testAlgSHA256},
			expectedCommands:     shNopFailureCommands(testRVName),
			expectedReconcileErr: errors.New("ExitErr"),
		},
		{
			name:                 "adjust failure reported",
			rv:                   readyRVWithConfig(testRVSecret, testAlgSHA256, 4, false),
			rvr:                  disklessRVR(testRVRAltName, addr(testNodeIPv4, port(11))),
			needsResourcesDir:    true,
			cryptoAlgs:           []string{testAlgSHA256},
			expectedCommands:     adjustFailureCommands(testRVName),
			expectedReconcileErr: errors.New("adjusting the resource '" + testRVName + "': ExitErr"),
		},
		{
			name:                 "create-md failure reported",
			rv:                   readyRVWithConfig(testRVSecret, testAlgSHA256, 6, false),
			rvr:                  diskfulRVR(testRVRAltName, addr(testNodeIPv4, port(12)), testLLVName),
			llv:                  newLLV(testLLVName, testLVGName, testDiskName),
			lvg:                  newLVG(testLVGName),
			needsResourcesDir:    true,
			cryptoAlgs:           []string{testAlgSHA256},
			expectedCommands:     createMDFailureCommands(testRVName),
			expectedReconcileErr: errors.New("dumping metadata: ExitErr"),
		},
		{
			name:              "diskful with peers skips createMD and still adjusts",
			rv:                readyRVWithConfig(testRVSecret, testAlgSHA256, 5, false),
			rvr:               diskfulRVR(testRVRAltName, addr(testNodeIPv4, port(102)), testLLVName, peersFrom(peerDiskfulSpec(testPeerNodeName, testPeerNodeID, addr(testPeerIPv4, port(3))))),
			llv:               newLLV(testLLVName, testLVGName, testDiskName),
			lvg:               newLVG(testLVGName),
			needsResourcesDir: true,
			cryptoAlgs:        []string{testAlgSHA256},
			expectedCommands:  diskfulExpectedCommandsWithExistingMetadata(testRVName),
			postCheck: func(t *testing.T, cl client.Client) {
				rvr := fetchRVR(t, cl, testRVRAltName)
				expectTrue(t, rvr.Status.DRBD.Actual.InitialSyncCompleted, "initial sync completed")
				expectString(t, rvr.Status.DRBD.Actual.Disk, "/dev/"+testLVGName+"/"+testDiskName, "actual disk")
			},
		},
		{
			name:                 "unsupported crypto algorithm surfaces error",
			rv:                   readyRVWithConfig(testRVSecret, testAlgUnsupported, 3, false),
			rvr:                  disklessRVR(testRVRAltName, addr(testNodeIPv4, port(200))),
			needsResourcesDir:    true,
			cryptoAlgs:           []string{testAlgSHA256},
			expectedReconcileErr: errors.New("shared secret alg is unsupported by the kernel: " + testAlgUnsupported),
			postCheck: func(t *testing.T, cl client.Client) {
				rvr := fetchRVR(t, cl, testRVRAltName)
				if rvr.Status.DRBD.Errors == nil || rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError == nil {
					t.Fatalf("expected shared secret alg selection error recorded")
				}
			},
		},
		{
			name:              "crypto algorithm matching is case insensitive (uppercase in config, lowercase in kernel)",
			rv:                readyRVWithConfig(testRVSecret, "SHA256", 7, false),
			rvr:               disklessRVR(testRVRName, addr(testNodeIPv4, port(201)), peersFrom(peerDisklessSpec(testPeerNodeName, testPeerNodeID, addr(testPeerIPv4, port(202))))),
			needsResourcesDir: true,
			cryptoAlgs:        []string{"sha256"}, // lowercase in kernel
			expectedCommands:  disklessExpectedCommands(testRVName),
			postCheck: func(t *testing.T, cl client.Client) {
				rvr := fetchRVR(t, cl, testRVRName)
				expectFinalizers(t, rvr.Finalizers, v1alpha1.AgentAppFinalizer, v1alpha1.ControllerAppFinalizer)
				expectNoDRBDErrors(t, rvr.Status.DRBD.Errors)
			},
		},
	}

	setupMemFS(t)
	setupDiscardLogger(t)

	scheme, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		t.Run(
			tc.name,
			func(t *testing.T) {
				resetMemFS(t)
				if tc.needsResourcesDir {
					ensureResourcesDir(t)
				}
				if len(tc.cryptoAlgs) > 0 {
					writeCryptoFile(t, tc.cryptoAlgs...)
				}
				if tc.prepare != nil {
					tc.prepare(t)
				}

				cl := fake.NewClientBuilder().
					WithScheme(scheme).
					WithStatusSubresource(
						&v1alpha1.ReplicatedVolumeReplica{},
						&v1alpha1.ReplicatedVolume{},
					).
					WithObjects(tc.toObjects()...).
					Build()

				fakeExec := &fakedrbdadm.Exec{}
				fakeExec.ExpectCommands(tc.expectedCommands...)
				fakeExec.Setup(t)

				rec := drbdconfig.NewReconciler(cl, nil, testNodeName)

				_, err := rec.Reconcile(
					t.Context(),
					reconcile.Request{
						NamespacedName: types.NamespacedName{Name: tc.rv.Name},
					},
				)

				if (err == nil) != (tc.expectedReconcileErr == nil) ||
					(err != nil && err.Error() != tc.expectedReconcileErr.Error()) {
					t.Errorf("expected reconcile error to be '%v', got '%v'", tc.expectedReconcileErr, err)
				}

				if tc.postCheck != nil {
					tc.postCheck(t, cl)
				}
			},
		)
	}
}

func (tc *reconcileTestCase) toObjects() (res []client.Object) {
	res = append(res, tc.rv) // rv required
	if tc.rvr != nil {
		res = append(res, tc.rvr)
	}
	res = append(res, tc.objs...)
	if tc.llv != nil {
		res = append(res, tc.llv)
	}
	if tc.lvg != nil {
		res = append(res, tc.lvg)
	}
	return res
}

func testRV() *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:       testRVName,
			Finalizers: []string{v1alpha1.ControllerAppFinalizer},
		},
	}
}

func rvWithoutSecret() *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:       testRVName,
			Finalizers: []string{v1alpha1.ControllerAppFinalizer},
		},
		Status: &v1alpha1.ReplicatedVolumeStatus{
			DRBD: &v1alpha1.DRBDResource{
				Config: &v1alpha1.DRBDResourceConfig{},
			},
		},
	}
}

func port(offset uint) uint {
	return testPortBase + offset
}

func rvrSpecOnly(name string, rvrType string) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: testRVName,
			NodeName:             testNodeName,
			Type:                 rvrType,
		},
	}
}

func disklessRVR(name string, address v1alpha1.Address, peers ...map[string]v1alpha1.Peer) *v1alpha1.ReplicatedVolumeReplica {
	return readyRVR(name, rvrTypeAccess, testNodeIDLocal, address, firstMapOrNil(peers), "")
}

//nolint:unparam // accepts name for readability and potential future cases
func diskfulRVR(name string, address v1alpha1.Address, llvName string, peers ...map[string]v1alpha1.Peer) *v1alpha1.ReplicatedVolumeReplica {
	return readyRVR(name, rvrTypeDiskful, testNodeIDLocal, address, firstMapOrNil(peers), llvName)
}

func firstMapOrNil(ms []map[string]v1alpha1.Peer) map[string]v1alpha1.Peer {
	if len(ms) == 0 {
		return nil
	}
	return ms[0]
}

func rvrWithErrors(rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.ReplicatedVolumeReplica {
	r := rvr.DeepCopy()
	if r.Status == nil {
		r.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}
	if r.Status.DRBD == nil {
		r.Status.DRBD = &v1alpha1.DRBD{}
	}
	r.Status.DRBD.Errors = &v1alpha1.DRBDErrors{
		FileSystemOperationError: &v1alpha1.MessageError{Message: "old-fs-error"},
		ConfigurationCommandError: &v1alpha1.CmdError{
			Command:  "old-cmd",
			Output:   "old-output",
			ExitCode: 1,
		},
	}
	return r
}

func resetMemFS(t *testing.T) {
	t.Helper()
	drbdconfig.FS = &afero.Afero{Fs: afero.NewMemMapFs()}
}

func ensureResourcesDir(t *testing.T) {
	t.Helper()
	if err := drbdconfig.FS.MkdirAll(drbdconfig.ResourcesDir, 0o755); err != nil {
		t.Fatalf("preparing resources dir: %v", err)
	}
}

func writeCryptoFile(t *testing.T, algs ...string) {
	t.Helper()

	if err := drbdconfig.FS.MkdirAll("/proc", 0o755); err != nil {
		t.Fatalf("preparing /proc: %v", err)
	}

	var b strings.Builder
	for _, alg := range algs {
		b.WriteString("name         : " + alg + "\n\n")
	}

	if err := drbdconfig.FS.WriteFile("/proc/crypto", []byte(b.String()), 0o644); err != nil {
		t.Fatalf("writing /proc/crypto: %v", err)
	}
}

//nolint:unparam // keep secret configurable for future scenarios
func readyRVWithConfig(secret, alg string, deviceMinor uint, allowTwoPrimaries bool) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:       testRVName,
			Finalizers: []string{v1alpha1.ControllerAppFinalizer},
		},
		Status: &v1alpha1.ReplicatedVolumeStatus{
			DRBD: &v1alpha1.DRBDResource{
				Config: &v1alpha1.DRBDResourceConfig{
					SharedSecret:            secret,
					SharedSecretAlg:         v1alpha1.SharedSecretAlg(alg),
					AllowTwoPrimaries:       allowTwoPrimaries,
					DeviceMinor:             &deviceMinor,
					Quorum:                  1,
					QuorumMinimumRedundancy: 1,
				},
			},
		},
	}
}

func readyRVR(
	name string,
	rvrType string,
	nodeID uint,
	address v1alpha1.Address,
	peers map[string]v1alpha1.Peer,
	lvmLogicalVolumeName string,
) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: testRVName,
			NodeName:             testNodeName,
			Type:                 rvrType,
		},
		Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
			LVMLogicalVolumeName: lvmLogicalVolumeName,
			DRBD: &v1alpha1.DRBD{
				Config: &v1alpha1.DRBDConfig{
					NodeId:           &nodeID,
					Address:          &address,
					Peers:            peers,
					PeersInitialized: true,
				},
				Actual: &v1alpha1.DRBDActual{},
			},
		},
	}
}

func deletingRVR(name, llvName string) *v1alpha1.ReplicatedVolumeReplica {
	now := v1.NewTime(time.Now())

	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: v1.ObjectMeta{
			Name:              name,
			Finalizers:        []string{v1alpha1.AgentAppFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: testRVName,
			NodeName:             testNodeName,
			Type:                 rvrTypeDiskful,
		},
		Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
			LVMLogicalVolumeName: llvName,
			DRBD: &v1alpha1.DRBD{
				Config: &v1alpha1.DRBDConfig{
					NodeId:           ptrUint(0),
					Address:          &v1alpha1.Address{IPv4: testNodeIPv4, Port: port(3)},
					PeersInitialized: true,
				},
				Actual: &v1alpha1.DRBDActual{},
			},
		},
	}
}

//nolint:unparam // keep name configurable for clarity and reuse
func newLLV(name, lvgName, lvName string) *snc.LVMLogicalVolume {
	return &snc.LVMLogicalVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:       name,
			Finalizers: []string{v1alpha1.AgentAppFinalizer},
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: lvName,
			Type:                  "thin",
			Size:                  "1Gi",
			LVMVolumeGroupName:    lvgName,
			Source: &snc.LVMLogicalVolumeSource{
				Kind: "LVMVolumeGroup",
				Name: lvgName,
			},
			Thin: &snc.LVMLogicalVolumeThinSpec{
				PoolName: "pool",
			},
		},
	}
}

func newLVG(name string) *snc.LVMVolumeGroup {
	return &snc.LVMVolumeGroup{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMVolumeGroupSpec{
			ActualVGNameOnTheNode: name,
			Type:                  "local",
			Local: snc.LVMVolumeGroupLocalSpec{
				NodeName: testNodeName,
			},
		},
	}
}

func newExpectedCmd(name string, args []string, output string, err error) *fakedrbdadm.ExpectedCmd {
	return &fakedrbdadm.ExpectedCmd{
		Name:         name,
		Args:         args,
		ResultOutput: []byte(output),
		ResultErr:    err,
	}
}

func disklessExpectedCommands(rvName string) []*fakedrbdadm.ExpectedCmd {
	regular, tmp := drbdconfig.FilePaths(rvName)

	return []*fakedrbdadm.ExpectedCmd{
		newExpectedCmd(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), "ok", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.StatusArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.AdjustArgs(rvName), "", nil),
	}
}

func diskfulExpectedCommands(rvName string) []*fakedrbdadm.ExpectedCmd {
	regular, tmp := drbdconfig.FilePaths(rvName)

	return []*fakedrbdadm.ExpectedCmd{
		newExpectedCmd(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), "", nil),
		{
			Name:         drbdadm.Command,
			Args:         drbdadm.DumpMDArgs(rvName),
			ResultOutput: []byte("No valid meta data found"),
			ResultErr:    fakedrbdadm.ExitErr{Code: 1},
		},
		newExpectedCmd(drbdadm.Command, drbdadm.CreateMDArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.PrimaryForceArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.SecondaryArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.StatusArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.AdjustArgs(rvName), "", nil),
	}
}

func ptrUint(v uint) *uint {
	return &v
}

func addr(ip string, port uint) v1alpha1.Address {
	return v1alpha1.Address{IPv4: ip, Port: port}
}

type peerSpec struct {
	name     string
	nodeID   uint
	address  v1alpha1.Address
	diskless bool
}

func peerDisklessSpec(name string, nodeID uint, address v1alpha1.Address) peerSpec {
	return peerSpec{name: name, nodeID: nodeID, address: address, diskless: true}
}

func peerDiskfulSpec(name string, nodeID uint, address v1alpha1.Address) peerSpec {
	return peerSpec{name: name, nodeID: nodeID, address: address, diskless: false}
}

func peersFrom(specs ...peerSpec) map[string]v1alpha1.Peer {
	peers := make(map[string]v1alpha1.Peer, len(specs))
	for _, spec := range specs {
		peers[spec.name] = v1alpha1.Peer{
			NodeId:   spec.nodeID,
			Address:  spec.address,
			Diskless: spec.diskless,
		}
	}
	return peers
}

func diskfulExpectedCommandsWithExistingMetadata(rvName string) []*fakedrbdadm.ExpectedCmd {
	regular, tmp := drbdconfig.FilePaths(rvName)

	return []*fakedrbdadm.ExpectedCmd{
		newExpectedCmd(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.DumpMDArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.StatusArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.AdjustArgs(rvName), "", nil),
	}
}

func fetchRVR(t *testing.T, cl client.Client, name string) *v1alpha1.ReplicatedVolumeReplica {
	t.Helper()
	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	if err := cl.Get(t.Context(), types.NamespacedName{Name: name}, rvr); err != nil {
		t.Fatalf("getting rvr %s: %v", name, err)
	}
	return rvr
}

func tryGetRVR(t *testing.T, cl client.Client, name string) (*v1alpha1.ReplicatedVolumeReplica, error) {
	t.Helper()
	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	return rvr, cl.Get(t.Context(), types.NamespacedName{Name: name}, rvr)
}

func tryGetLLV(t *testing.T, cl client.Client, name string) (*snc.LVMLogicalVolume, error) {
	t.Helper()
	llv := &snc.LVMLogicalVolume{}
	return llv, cl.Get(t.Context(), client.ObjectKey{Name: name}, llv)
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

func expectFileAbsent(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		exists, err := drbdconfig.FS.Exists(path)
		if err != nil {
			t.Fatalf("checking file %s: %v", path, err)
		}
		if exists {
			t.Fatalf("expected file %s to be removed", path)
		}
	}
}

func expectTrue(t *testing.T, condition bool, name string) {
	t.Helper()
	if !condition {
		t.Fatalf("expected %s to be true", name)
	}
}

func expectString(t *testing.T, got string, expected string, name string) {
	t.Helper()
	if got != expected {
		t.Fatalf("expected %s to be %q, got %q", name, expected, got)
	}
}

func expectNoDRBDErrors(t *testing.T, errs *v1alpha1.DRBDErrors) {
	t.Helper()
	if errs == nil {
		return
	}
	if errs.FileSystemOperationError != nil ||
		errs.ConfigurationCommandError != nil ||
		errs.SharedSecretAlgSelectionError != nil ||
		errs.LastPrimaryError != nil ||
		errs.LastSecondaryError != nil {
		t.Fatalf("expected no drbd errors, got %+v", errs)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := drbdconfig.FS.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func notFoundErr(resource, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: apiGroupStorage, Resource: resource}, name)
}

func selectErr(prefix, resource, name string) error {
	return fmt.Errorf("getting %s: %w", prefix, notFoundErr(resource, name))
}

func shNopFailureCommands(rvName string) []*fakedrbdadm.ExpectedCmd {
	regular, tmp := drbdconfig.FilePaths(rvName)
	return []*fakedrbdadm.ExpectedCmd{
		newExpectedCmd(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), "", fakedrbdadm.ExitErr{Code: 1}),
	}
}

func adjustFailureCommands(rvName string) []*fakedrbdadm.ExpectedCmd {
	regular, tmp := drbdconfig.FilePaths(rvName)
	return []*fakedrbdadm.ExpectedCmd{
		newExpectedCmd(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.StatusArgs(rvName), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.AdjustArgs(rvName), "", fakedrbdadm.ExitErr{Code: 1}),
	}
}

func createMDFailureCommands(rvName string) []*fakedrbdadm.ExpectedCmd {
	regular, tmp := drbdconfig.FilePaths(rvName)
	return []*fakedrbdadm.ExpectedCmd{
		newExpectedCmd(drbdadm.Command, drbdadm.ShNopArgs(tmp, regular), "", nil),
		newExpectedCmd(drbdadm.Command, drbdadm.DumpMDArgs(rvName), "", fakedrbdadm.ExitErr{Code: 2}),
	}
}
