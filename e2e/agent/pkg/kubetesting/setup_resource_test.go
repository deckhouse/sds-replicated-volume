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

package kubetesting_test

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

const (
	testName      = "test-cm"
	testNamespace = "default"
	testTimeout   = 5 * time.Second
	testFinalizer = "test.example.com/cleanup"
)

var testKey = client.ObjectKey{Name: testName, Namespace: testNamespace}

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func newConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNamespace},
		Data:       data,
	}
}

func getConfigMap(t *testing.T, cl client.Client) *corev1.ConfigMap {
	t.Helper()
	var cm corev1.ConfigMap
	if err := cl.Get(context.Background(), testKey, &cm); err != nil {
		return nil
	}
	return &cm
}

func mustGetConfigMap(t *testing.T, cl client.Client) *corev1.ConfigMap {
	t.Helper()
	cm := getConfigMap(t, cl)
	if cm == nil {
		t.Fatal("resource not found")
	}
	return cm
}

type testFixture struct {
	t      *testing.T
	scheme *runtime.Scheme
	client client.Client
	wc     *fakeWithWatch
	fw     *syncFakeWatch
	e      *testE
}

func newFixture(t *testing.T, existingObjects ...client.Object) *testFixture {
	scheme := testScheme()
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(existingObjects) > 0 {
		builder = builder.WithObjects(existingObjects...)
	}
	cl := builder.Build()
	return &testFixture{
		t:      t,
		scheme: scheme,
		client: cl,
		wc:     &fakeWithWatch{Client: cl, scheme: scheme},
		e:      newTestE(t),
	}
}

func (f *testFixture) withSyncWatch() *testFixture {
	f.fw = newSyncFakeWatch()
	f.wc.watchFn = f.fw.Watch
	return f
}

func (f *testFixture) runAsync(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

func TestSetupResource_WithoutPredicate(t *testing.T) {
	f := newFixture(t)
	cm := newConfigMap(map[string]string{"key": "value"})

	result := kubetesting.SetupResource(f.e, f.wc, cm, nil)

	if result.Name != testName {
		t.Errorf("expected name %s, got %s", testName, result.Name)
	}
	if result.Data["key"] != "value" {
		t.Errorf("expected data key=value, got %v", result.Data)
	}
	mustGetConfigMap(t, f.client)

	if len(f.e.cleanups) != 1 {
		t.Errorf("expected 1 cleanup, got %d", len(f.e.cleanups))
	}

	f.e.Close()

	if cm := getConfigMap(t, f.client); cm != nil {
		t.Error("expected resource to be deleted after cleanup")
	}
}

func TestSetupResource_WithPredicate(t *testing.T) {
	f := newFixture(t).withSyncWatch()
	cm := newConfigMap(map[string]string{"key": "initial"})

	var result *corev1.ConfigMap
	done := f.runAsync(func() {
		result = kubetesting.SetupResource(f.e.ScopeWithTimeout(testTimeout), f.wc, cm, func(cm *corev1.ConfigMap) bool {
			return cm.Data["key"] == "updated"
		})
	})

	f.fw.WaitForWatch()
	f.fw.Modify(newConfigMap(map[string]string{"key": "updated"}))
	<-done

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Data["key"] != "updated" {
		t.Errorf("expected key=updated, got %v", result.Data)
	}
}

func TestSetupResource_CleanupLeavesControllerFinalizer(t *testing.T) {
	f := newFixture(t)
	cm := newConfigMap(nil)
	cm.Finalizers = []string{testFinalizer}
	kubetesting.SetupResource(f.e, f.wc, cm, nil)

	cm = mustGetConfigMap(t, f.client)
	cm.Finalizers = append(cm.Finalizers, "controller-finalizer")
	_ = f.client.Update(context.Background(), cm)

	f.e.Close()

	cm = getConfigMap(t, f.client)
	if cm == nil {
		t.Fatal("expected resource to still exist (controller finalizer blocks deletion)")
	}
	if len(cm.Finalizers) != 1 || cm.Finalizers[0] != "controller-finalizer" {
		t.Errorf("expected only controller-finalizer, got %v", cm.Finalizers)
	}
	if cm.DeletionTimestamp == nil {
		t.Error("expected deletionTimestamp to be set")
	}
}

func TestSetupResourcePatch_WithoutPredicate(t *testing.T) {
	f := newFixture(t, newConfigMap(map[string]string{"key": "original"}))

	result := kubetesting.SetupResourcePatch(f.e, f.wc, testKey, func(cm *corev1.ConfigMap) {
		cm.Data["key"] = "patched"
	}, nil)

	if result.Data["key"] != "patched" {
		t.Errorf("expected key=patched, got %v", result.Data)
	}

	cm := mustGetConfigMap(t, f.client)
	if cm.Data["key"] != "patched" {
		t.Errorf("expected patched in cluster, got %v", cm.Data)
	}

	if len(f.e.cleanups) != 1 {
		t.Errorf("expected 1 cleanup, got %d", len(f.e.cleanups))
	}

	f.e.Close()

	cm = mustGetConfigMap(t, f.client)
	if cm.Data["key"] != "original" {
		t.Errorf("expected revert to original, got %v", cm.Data)
	}
}

func TestSetupResourcePatch_WithPredicate(t *testing.T) {
	f := newFixture(t, newConfigMap(map[string]string{"key": "original"})).withSyncWatch()

	var result *corev1.ConfigMap
	done := f.runAsync(func() {
		result = kubetesting.SetupResourcePatch(f.e.ScopeWithTimeout(testTimeout), f.wc, testKey,
			func(cm *corev1.ConfigMap) { cm.Data["key"] = "patched" },
			func(cm *corev1.ConfigMap) bool { return cm.Data["status"] == "ready" })
	})

	f.fw.WaitForWatch()
	f.fw.Modify(newConfigMap(map[string]string{"key": "patched", "status": "ready"}))
	<-done

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Data["status"] != "ready" {
		t.Errorf("expected status=ready, got %v", result.Data)
	}
}

func TestSetupResourcePatch_RevertOnCleanup(t *testing.T) {
	f := newFixture(t, newConfigMap(map[string]string{"a": "1", "b": "2"}))

	kubetesting.SetupResourcePatch(f.e, f.wc, testKey, func(cm *corev1.ConfigMap) {
		cm.Data["a"] = "changed"
		cm.Data["c"] = "new"
	}, nil)

	cm := mustGetConfigMap(t, f.client)
	if cm.Data["a"] != "changed" || cm.Data["c"] != "new" {
		t.Errorf("patch not applied: %v", cm.Data)
	}

	f.e.Close()

	cm = mustGetConfigMap(t, f.client)
	if cm.Data["a"] != "1" || cm.Data["b"] != "2" {
		t.Errorf("expected revert to a=1,b=2, got %v", cm.Data)
	}
}

// syncFakeWatch is a fake watcher with synchronization for tests.
type syncFakeWatch struct {
	watcher   *watch.FakeWatcher
	watchOnce sync.Once
	watchCh   chan struct{}
}

func newSyncFakeWatch() *syncFakeWatch {
	return &syncFakeWatch{
		watcher: watch.NewFake(),
		watchCh: make(chan struct{}),
	}
}

func (f *syncFakeWatch) Watch(context.Context, client.ObjectList, ...client.ListOption) (watch.Interface, error) {
	f.watchOnce.Do(func() { close(f.watchCh) })
	return f.watcher, nil
}

func (f *syncFakeWatch) WaitForWatch() { <-f.watchCh }

func (f *syncFakeWatch) Modify(obj runtime.Object) { f.watcher.Modify(obj) }

// fakeWithWatch wraps fake.Client to implement client.WithWatch.
type fakeWithWatch struct {
	client.Client
	scheme  *runtime.Scheme
	watchFn func(context.Context, client.ObjectList, ...client.ListOption) (watch.Interface, error)
}

func (f *fakeWithWatch) Scheme() *runtime.Scheme { return f.scheme }

func (f *fakeWithWatch) Watch(ctx context.Context, list client.ObjectList, opts ...client.ListOption) (watch.Interface, error) {
	if f.watchFn != nil {
		return f.watchFn(ctx, list, opts...)
	}
	return watch.NewFake(), nil
}

// testE is a minimal implementation of envtesting.E for testing.
type testE struct {
	t        *testing.T
	ctx      context.Context
	cancel   context.CancelFunc
	cleanups []func()
	mu       sync.Mutex
	failed   bool
}

func newTestE(t *testing.T) *testE {
	ctx, cancel := context.WithCancel(context.Background())
	return &testE{t: t, ctx: ctx, cancel: cancel}
}

func (e *testE) Fatal(args ...any)                 { e.t.Fatal(args...) }
func (e *testE) Fatalf(format string, args ...any) { e.t.Fatalf(format, args...) }
func (e *testE) FailNow()                          { e.t.FailNow() }
func (e *testE) Log(args ...any)                   { e.t.Log(args...) }
func (e *testE) Logf(format string, args ...any)   { e.t.Logf(format, args...) }
func (e *testE) Helper()                           { e.t.Helper() }
func (e *testE) Skip(args ...any)                  { e.t.Skip(args...) }
func (e *testE) Skipf(format string, args ...any)  { e.t.Skipf(format, args...) }
func (e *testE) SkipNow()                          { e.t.SkipNow() }
func (e *testE) Name() string                      { return e.t.Name() }
func (e *testE) Parallel()                         { e.t.Parallel() }
func (e *testE) Setenv(key, value string)          { e.t.Setenv(key, value) }
func (e *testE) Skipped() bool                     { return e.t.Skipped() }
func (e *testE) TempDir() string                   { return e.t.TempDir() }
func (e *testE) Deadline() (time.Time, bool)       { return e.t.Deadline() }
func (e *testE) Options(...any)                    {}
func (e *testE) Run(name string, fn func(envtesting.E)) bool {
	return e.t.Run(name, func(t *testing.T) { fn(newTestE(t)) })
}
func (e *testE) Context() context.Context { return e.ctx }

func (e *testE) Cleanup(f func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanups = append(e.cleanups, f)
}

func (e *testE) Error(...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failed = true
}

func (e *testE) Errorf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failed = true
	e.t.Logf(format, args...)
}

func (e *testE) Fail() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failed = true
}

func (e *testE) Failed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.failed
}

func (e *testE) Scope() envtesting.E {
	ctx, cancel := context.WithCancel(e.ctx)
	child := &testE{t: e.t, ctx: ctx, cancel: cancel}
	e.Cleanup(child.Close)
	return child
}

func (e *testE) ScopeWithTimeout(timeout time.Duration) envtesting.E {
	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	child := &testE{t: e.t, ctx: ctx, cancel: cancel}
	e.Cleanup(child.Close)
	return child
}

func (e *testE) DiscardErrors() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failed = false
}

func (e *testE) Close() {
	e.cancel()
	e.mu.Lock()
	cleanups := e.cleanups
	e.cleanups = nil
	e.mu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

var (
	_ envtesting.E     = (*testE)(nil)
	_ client.WithWatch = (*fakeWithWatch)(nil)
)
