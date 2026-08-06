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

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	commonsync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
)

func TestUpgradeStopsAtPreflight(t *testing.T) {
	withStaleModulesLoaded(t)
	withFakeModuleDirs(t) // empty: no module files at all

	u := newTestUpgrader(t, true, &failOnUseReader{t: t})

	err := u.EnsureUpgraded(context.Background())
	if err == nil {
		t.Fatal("EnsureUpgraded() error = nil; want a preflight error")
	}
	if !errors.Is(err, ErrPreflight) {
		t.Errorf("EnsureUpgraded() error %v does not wrap ErrPreflight", err)
	}

	if err := u.EnsureUpgraded(context.Background()); !errors.Is(err, ErrPreflight) {
		t.Errorf("EnsureUpgraded() on retry: error = %v; want it to attempt again and fail preflight", err)
	}
}

func TestUpgradeNotNeeded(t *testing.T) {
	withFakeModuleDirs(t) // empty: preflight would fail if it ran

	u := newTestUpgrader(t, false, &failOnUseReader{t: t})

	if err := u.EnsureUpgraded(context.Background()); err != nil {
		t.Errorf("EnsureUpgraded() error = %v; want nil when no upgrade is needed", err)
	}
}

// Freezing I/O is only justified by taking a wrongly-versioned module out of the
// kernel; a module that is merely absent gets a plain load.
func TestPlanModules(t *testing.T) {
	const otherVersion = "9.2.18-flant.9"

	tests := []struct {
		name string
		// module name -> version reported in sysfs; absent means not loaded,
		// noVersion means loaded without a version file
		running    map[string]string
		wantUnload []string
		wantLoad   []string
	}{
		{
			name:       "nothing loaded",
			running:    nil,
			wantUnload: nil,
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
		{
			name:       "core current, transport not loaded yet",
			running:    map[string]string{"drbd": TargetDRBDVersion},
			wantUnload: nil,
			wantLoad:   []string{"drbd_transport_tcp"},
		},
		{
			name: "both current",
			running: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			wantUnload: nil,
			wantLoad:   nil,
		},
		{
			name: "core stale",
			running: map[string]string{
				"drbd":               otherVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
			wantUnload: []string{"drbd_transport_tcp", "drbd"},
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
		{
			name:       "core stale, transport not loaded",
			running:    map[string]string{"drbd": otherVersion},
			wantUnload: []string{"drbd"},
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
		{
			name: "transport stale",
			running: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": otherVersion,
			},
			wantUnload: []string{"drbd_transport_tcp", "drbd"},
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
		{
			name:       "loaded but declaring no version",
			running:    map[string]string{"drbd": noVersion},
			wantUnload: []string{"drbd"},
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeSysModuleDir(t, tt.running)
			writeAllModuleFiles(t)

			verified, err := preflight(discardLogger())
			if err != nil {
				t.Fatalf("preflight() error = %v", err)
			}
			running, err := runningModules()
			if err != nil {
				t.Fatalf("runningModules() error = %v", err)
			}

			plan := planModules(discardLogger(), verified, running)

			if !slices.Equal(plan.unload, tt.wantUnload) {
				t.Errorf("plan.unload = %v; want %v", plan.unload, tt.wantUnload)
			}
			load := make([]string, 0, len(plan.load))
			for _, m := range plan.load {
				load = append(load, m.name)
			}
			if !slices.Equal(load, tt.wantLoad) {
				t.Errorf("plan.load = %v; want %v", load, tt.wantLoad)
			}
		})
	}
}

// A plain load takes nothing away from the kernel, so there is nothing to look up.
func TestPrepareSkipsAPIWhenNothingIsUnloaded(t *testing.T) {
	tests := []struct {
		name    string
		running map[string]string
	}{
		{name: "nothing loaded", running: nil},
		{name: "core current, transport not loaded yet", running: map[string]string{"drbd": TargetDRBDVersion}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeSysModuleDir(t, tt.running)
			writeAllModuleFiles(t)

			plan, err := prepare(context.Background(), discardLogger(), &failOnUseReader{t: t}, "test-node")
			if err != nil {
				t.Fatalf("prepare() error = %v", err)
			}

			if len(plan.unload) != 0 {
				t.Errorf("plan.unload = %v; want nothing unloaded", plan.unload)
			}
			if len(plan.paired) != 0 || len(plan.unpaired) != 0 {
				t.Errorf("plan has %d paired and %d unpaired resources; want nothing to suspend or bring down",
					len(plan.paired), len(plan.unpaired))
			}
			if len(plan.load) == 0 {
				t.Error("plan.load is empty; want the absent modules queued for loading")
			}
		})
	}
}

func TestApplyModuleSequence(t *testing.T) {
	tests := []struct {
		name       string
		plan       *upgradePlan
		wantDelete []string
		wantLoad   []string
	}{
		{
			name: "plain load of both modules",
			plan: &upgradePlan{
				load: []plannedModule{{name: "drbd", path: "/drbd.ko"}, {name: "drbd_transport_tcp", path: "/tcp.ko"}},
			},
			wantDelete: nil,
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
		{
			name: "plain load of the transport alone",
			plan: &upgradePlan{
				load: []plannedModule{{name: "drbd_transport_tcp", path: "/tcp.ko"}},
			},
			wantDelete: nil,
			wantLoad:   []string{"drbd_transport_tcp"},
		},
		{
			name: "replacing both modules",
			plan: &upgradePlan{
				unload: []string{"drbd_transport_tcp", "drbd"},
				load:   []plannedModule{{name: "drbd", path: "/drbd.ko"}, {name: "drbd_transport_tcp", path: "/tcp.ko"}},
			},
			wantDelete: []string{"drbd_transport_tcp", "drbd"},
			wantLoad:   []string{"drbd", "drbd_transport_tcp"},
		},
		{
			name:       "nothing to do",
			plan:       &upgradePlan{},
			wantDelete: nil,
			wantLoad:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleted, loaded := withFakeModuleSyscalls(t)

			if err := apply(context.Background(), discardLogger(), tt.plan); err != nil {
				t.Fatalf("apply() error = %v", err)
			}

			if !slices.Equal(*deleted, tt.wantDelete) {
				t.Errorf("deleted modules = %v; want %v", *deleted, tt.wantDelete)
			}
			if !slices.Equal(*loaded, tt.wantLoad) {
				t.Errorf("loaded modules = %v; want %v", *loaded, tt.wantLoad)
			}
		})
	}
}

func TestApplyReportsLoadFailure(t *testing.T) {
	withFakeModuleSyscalls(t)
	failure := errors.New("invalid module format")
	loadModule = func(plannedModule) error { return failure }

	plan := &upgradePlan{load: []plannedModule{{name: "drbd", path: "/drbd.ko"}}}
	if err := apply(context.Background(), discardLogger(), plan); !errors.Is(err, failure) {
		t.Errorf("apply() error = %v; want it to wrap %v", err, failure)
	}
}

// An upgrade that can never complete must fail startup rather than block
// reconciliation silently.
func TestInitializeUpgraderFailsWithoutModuleFiles(t *testing.T) {
	tests := []struct {
		name string
		// module name -> version reported in sysfs
		running map[string]string
	}{
		{name: "nothing loaded", running: nil},
		{name: "drbd at an older version", running: map[string]string{"drbd": "9.2.18-flant.9"}},
		{
			name: "transport not loaded",
			running: map[string]string{
				"drbd": TargetDRBDVersion,
			},
		},
		{
			name: "transport at an older version",
			running: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": "9.2.18-flant.9",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withSavedUpgrader(t)
			withFakeSysModuleDir(t, tt.running)
			withFakeModuleDirs(t) // empty: no module files at all

			err := InitializeUpgrader(discardLogger(), &failOnUseReader{t: t}, "test-node")
			if err == nil {
				t.Fatal("InitializeUpgrader() error = nil; want a preflight error")
			}
			if !errors.Is(err, ErrPreflight) {
				t.Errorf("InitializeUpgrader() error %v does not wrap ErrPreflight", err)
			}
		})
	}
}

func TestInitializeUpgraderSucceedsWithModuleFiles(t *testing.T) {
	tests := []struct {
		name    string
		running map[string]string
	}{
		{name: "nothing loaded", running: nil},
		{name: "drbd at an older version", running: map[string]string{"drbd": "9.2.18-flant.9"}},
		{
			name: "both at the target version",
			running: map[string]string{
				"drbd":               TargetDRBDVersion,
				"drbd_transport_tcp": TargetDRBDVersion,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withSavedUpgrader(t)
			withFakeSysModuleDir(t, tt.running)
			writeAllModuleFiles(t)

			if err := InitializeUpgrader(discardLogger(), &failOnUseReader{t: t}, "test-node"); err != nil {
				t.Fatalf("InitializeUpgrader() error = %v; want nil", err)
			}
			if Upgrader == nil {
				t.Fatal("InitializeUpgrader() left Upgrader nil")
			}
		})
	}
}

// Matching versions mean nothing gets loaded, so no module file is required.
func TestInitializeUpgraderSkipsPreflightWhenVersionsMatch(t *testing.T) {
	withSavedUpgrader(t)
	withAllModulesLoaded(t)
	withFakeModuleDirs(t) // empty: preflight would fail if it ran

	if err := InitializeUpgrader(discardLogger(), &failOnUseReader{t: t}, "test-node"); err != nil {
		t.Fatalf("InitializeUpgrader() error = %v; want nil", err)
	}
	if err := Upgrader.EnsureUpgraded(context.Background()); err != nil {
		t.Errorf("EnsureUpgraded() error = %v; want a disarmed upgrader", err)
	}
}

func TestInitializeUpgraderArmsUpgrade(t *testing.T) {
	withSavedUpgrader(t)
	withStaleModulesLoaded(t)
	writeAllModuleFiles(t)

	// Stops at the API listing, well before any module syscall.
	listErr := errors.New("listing refused")
	if err := InitializeUpgrader(discardLogger(), &errorReader{err: listErr}, "test-node"); err != nil {
		t.Fatalf("InitializeUpgrader() error = %v; want nil", err)
	}

	if err := Upgrader.EnsureUpgraded(context.Background()); !errors.Is(err, listErr) {
		t.Errorf("EnsureUpgraded() error = %v; want it to wrap %v", err, listErr)
	}
}

// A node waiting on its module file must be diagnosed by the module file, not by
// whatever the API happened to return.
func TestPrepareChecksModuleFilesFirst(t *testing.T) {
	withStaleModulesLoaded(t)
	release := withFakeModuleDirs(t)
	writeFakeModule(t, moduleDirs[0], release, "drbd", TargetDRBDVersion)
	// drbd_transport_tcp is deliberately absent.

	plan, err := prepare(context.Background(), discardLogger(), &failOnUseReader{t: t}, "test-node")
	if err == nil {
		t.Fatalf("prepare() = %+v, nil; want a preflight error", plan)
	}
	if !errors.Is(err, ErrPreflight) {
		t.Errorf("prepare() error %v does not wrap ErrPreflight", err)
	}
}

func TestPrepareSplitsPairedAndUnpairedResources(t *testing.T) {
	withStaleModulesLoaded(t)
	writeAllModuleFiles(t)

	const nodeName = "test-node"
	reader := &listReader{
		resources: []v1alpha1.DRBDResource{
			newDRBDResource("paired", nodeName, "/dev/drbd1000"),
			newDRBDResource("no-mapper", nodeName, "/dev/drbd1001"),
			// not in the kernel, so status.device is cleared
			newDRBDResource("no-device", nodeName, ""),
			newDRBDResource("other-node", "another-node", "/dev/drbd1002"),
		},
		mappers: []v1alpha1.DRBDMapper{
			newDRBDMapper("paired-mapper", nodeName, "/dev/drbd1000"),
			newDRBDMapper("other-node-mapper", "another-node", "/dev/drbd1002"),
		},
	}

	plan, err := prepare(context.Background(), discardLogger(), reader, nodeName)
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}

	if len(plan.paired) != 1 {
		t.Fatalf("plan.paired = %+v; want exactly the one resource with a mapper", plan.paired)
	}
	if got := plan.paired[0].resource.Name; got != "paired" {
		t.Errorf("plan.paired[0].resource = %q; want %q", got, "paired")
	}
	if got := plan.paired[0].mapper.Name; got != "paired-mapper" {
		t.Errorf("plan.paired[0].mapper = %q; want %q", got, "paired-mapper")
	}

	unpaired := make([]string, 0, len(plan.unpaired))
	for _, res := range plan.unpaired {
		unpaired = append(unpaired, res.Name)
	}
	want := []string{"no-mapper", "no-device"}
	if len(unpaired) != len(want) {
		t.Fatalf("plan.unpaired = %v; want %v", unpaired, want)
	}
	for i := range want {
		if unpaired[i] != want[i] {
			t.Fatalf("plan.unpaired = %v; want %v", unpaired, want)
		}
	}

	if !slices.Equal(plan.unload, []string{"drbd_transport_tcp", "drbd"}) {
		t.Errorf("plan.unload = %v; want both modules replaced", plan.unload)
	}
}

// --- fixtures ---

func newTestUpgrader(t *testing.T, needed bool, cl client.Reader) *commonsync.OnceUpgrader {
	t.Helper()

	return commonsync.NewOnceUpgrader(needed, func(ctx context.Context) error {
		return execute(ctx, discardLogger(), cl, "test-node")
	})
}

func withAllModulesLoaded(t *testing.T) {
	t.Helper()

	withFakeSysModuleDir(t, modulesAt(TargetDRBDVersion))
}

// withStaleModulesLoaded is the only state that makes the upgrade destructive.
func withStaleModulesLoaded(t *testing.T) {
	t.Helper()

	withFakeSysModuleDir(t, modulesAt("9.2.18-flant.9"))
}

func modulesAt(version string) map[string]string {
	running := make(map[string]string, len(moduleLoadOrder))
	for _, name := range moduleLoadOrder {
		running[name] = version
	}
	return running
}

func writeAllModuleFiles(t *testing.T) {
	t.Helper()

	release := withFakeModuleDirs(t)
	for _, name := range moduleLoadOrder {
		writeFakeModule(t, moduleDirs[0], release, name, TargetDRBDVersion)
	}
}

// withFakeModuleSyscalls records what apply would have asked the kernel to do.
func withFakeModuleSyscalls(t *testing.T) (deleted, loaded *[]string) {
	t.Helper()

	originalDelete, originalLoad := deleteModule, loadModule
	t.Cleanup(func() { deleteModule, loadModule = originalDelete, originalLoad })

	deleted, loaded = &[]string{}, &[]string{}
	deleteModule = func(name string) error {
		*deleted = append(*deleted, name)
		return nil
	}
	loadModule = func(m plannedModule) error {
		*loaded = append(*loaded, m.name)
		return nil
	}
	return deleted, loaded
}

func withSavedUpgrader(t *testing.T) {
	t.Helper()

	original := Upgrader
	t.Cleanup(func() { Upgrader = original })
}

func newDRBDResource(name, nodeName, device string) v1alpha1.DRBDResource {
	var res v1alpha1.DRBDResource
	res.Name = name
	res.Spec.NodeName = nodeName
	res.Status.Device = device
	return res
}

func newDRBDMapper(name, nodeName, lowerDevicePath string) v1alpha1.DRBDMapper {
	var mapper v1alpha1.DRBDMapper
	mapper.Name = name
	mapper.Spec.NodeName = nodeName
	mapper.Spec.LowerDevicePath = lowerDevicePath
	return mapper
}

type failOnUseReader struct {
	t *testing.T
}

var _ client.Reader = (*failOnUseReader)(nil)

func (r *failOnUseReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	r.t.Error("unexpected API Get before the module files were verified")
	return nil
}

func (r *failOnUseReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	r.t.Error("unexpected API List before the module files were verified")
	return nil
}

// errorReader fails the upgrade after preflight, short of the module syscalls.
type errorReader struct {
	err error
}

var _ client.Reader = (*errorReader)(nil)

func (r *errorReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return r.err
}

func (r *errorReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}

type listReader struct {
	resources []v1alpha1.DRBDResource
	mappers   []v1alpha1.DRBDMapper
}

var _ client.Reader = (*listReader)(nil)

func (r *listReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errors.New("not implemented")
}

func (r *listReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	switch typed := list.(type) {
	case *v1alpha1.DRBDResourceList:
		typed.Items = r.resources
	case *v1alpha1.DRBDMapperList:
		typed.Items = r.mappers
	default:
		return fmt.Errorf("unexpected list type %T", list)
	}
	return nil
}
