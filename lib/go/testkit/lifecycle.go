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

package testkit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

func mustParseRV(rv string) int64 {
	n, err := strconv.ParseInt(rv, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("mustParseRV: invalid resourceVersion %q: %v", rv, err))
	}
	return n
}

// DebugWatcher is an optional observer for lifecycle events.
// Implemented by the framework's Debugger — *dbg.Debugger satisfies
// this interface without changes.
type DebugWatcher interface {
	WatchRelated(obj client.Object) error
	UnwatchRelated(obj client.Object) error
}

// Lifecycle configures the Create/Get lifecycle for a TrackedObject.
// Passed to NewTrackedObject at construction time and stored as an
// unexported field. Zero value is safe: all nil/false fields mean
// "no informer management, no debugger, no build/empty hooks".
type Lifecycle[T client.Object] struct {
	// Debugger is an optional debug observer. When non-nil,
	// WatchRelated is called before the API call and UnwatchRelated
	// is called on error or during DeferCleanup.
	Debugger DebugWatcher

	// Standalone declares that this object manages its own informer.
	// When true, the lifecycle methods automatically call watchSelf
	// during setup and unwatchSelf during teardown.
	// Group-managed children (whose parent registers informers) leave
	// this false.
	Standalone bool

	// OnSetup is called during setup AFTER watchSelf (if Standalone).
	// Use for additional informer registration (group watches, extra
	// informers). Nil means no additional setup.
	OnSetup func(ctx context.Context)

	// OnTeardown is called during teardown BEFORE unwatchSelf
	// (if Standalone). Use for additional cleanup (group informers,
	// extra registrations). Nil means no additional teardown.
	OnTeardown func()

	// OnBuild constructs the API object from builder fields.
	// Called by CreateExpect and CreateOrGetExpect.
	// May be nil for objects that only use Get.
	OnBuild func(ctx context.Context) T

	// OnNewEmpty returns a fresh zero-value typed object for Get.
	// Needed because Go generics cannot instantiate pointer types.
	// May be nil for objects that only use Create.
	OnNewEmpty func() T
}

// setup runs the informer setup sequence: watchSelf (if Standalone),
// then OnSetup (if set).
func (t *TrackedObject[T]) setup(ctx context.Context) {
	if t.lifecycle.Standalone {
		t.watchSelf(ctx)
	}
	if t.lifecycle.OnSetup != nil {
		t.lifecycle.OnSetup(ctx)
	}
}

// teardown runs the cleanup sequence: OnTeardown (if set), then
// unwatchSelf (if Standalone).
func (t *TrackedObject[T]) teardown() {
	if t.lifecycle.OnTeardown != nil {
		t.lifecycle.OnTeardown()
	}
	if t.lifecycle.Standalone {
		t.unwatchSelf()
	}
}

// unwatchDebugger calls UnwatchRelated on the debugger if configured.
func (t *TrackedObject[T]) unwatchDebugger(obj client.Object) {
	if t.lifecycle.Debugger != nil {
		_ = t.lifecycle.Debugger.UnwatchRelated(obj)
	}
}

// CreateExpect builds the object via OnBuild, creates it on the cluster,
// and asserts the result against m. On success it registers DeferCleanup
// (delete + teardown + unwatch). On expected failure it tears down
// immediately.
//
// Informers and debugger are registered BEFORE the API call so that the
// tracker captures the full event history from the very first version.
//
// No-op when Client is nil (unit-test mode).
func (t *TrackedObject[T]) CreateExpect(ctx context.Context, m types.GomegaMatcher) {
	GinkgoHelper()
	if t.Client == nil {
		return
	}

	obj := t.lifecycle.OnBuild(ctx)
	obj.GetObjectKind().SetGroupVersionKind(t.GVK)

	if t.lifecycle.Debugger != nil {
		Expect(t.lifecycle.Debugger.WatchRelated(obj)).To(Succeed(),
			"watch related for %s %s", t.GVK.Kind, obj.GetName())
	}

	t.setup(ctx)

	fmt.Fprintf(GinkgoWriter, "[%s] creating %s %s\n",
		time.Now().Format("15:04:05.000"), t.GVK.Kind, obj.GetName())

	err := t.Client.Create(ctx, obj)
	Expect(err).To(m, "creating %s %s", t.GVK.Kind, obj.GetName())

	if err != nil {
		t.teardown()
		t.unwatchDebugger(obj)
	} else {
		t.Await(ctx, match.ResourceVersionAtLeast(obj.GetResourceVersion()))
		obj.GetObjectKind().SetGroupVersionKind(t.GVK)
		DeferCleanup(func(cleanupCtx context.Context) {
			if !t.IsDeleted() {
				t.deleteByName(cleanupCtx)
			}
			t.teardown()
			t.unwatchDebugger(obj)
			t.checkFailed()
		})
	}
}

// Create builds the object and creates it on the cluster, expecting success.
func (t *TrackedObject[T]) Create(ctx context.Context) {
	GinkgoHelper()
	t.CreateExpect(ctx, Succeed())
}

// CreateOrGetExpect ensures the object exists on the cluster: it builds
// via OnBuild and creates with IgnoreAlreadyExists semantics. The result
// is asserted against m. No DeferCleanup is registered — the caller
// manages the object's lifetime (typical for shared/suite-level objects).
//
// No-op when Client is nil (unit-test mode).
func (t *TrackedObject[T]) CreateOrGetExpect(ctx context.Context, m types.GomegaMatcher) {
	GinkgoHelper()
	if t.Client == nil {
		return
	}

	obj := t.lifecycle.OnBuild(ctx)
	obj.GetObjectKind().SetGroupVersionKind(t.GVK)

	if t.lifecycle.Debugger != nil {
		Expect(t.lifecycle.Debugger.WatchRelated(obj)).To(Succeed(),
			"watch related for %s %s", t.GVK.Kind, obj.GetName())
	}

	t.setup(ctx)

	fmt.Fprintf(GinkgoWriter, "[%s] ensuring %s %s\n",
		time.Now().Format("15:04:05.000"), t.GVK.Kind, obj.GetName())

	err := client.IgnoreAlreadyExists(t.Client.Create(ctx, obj))
	Expect(err).To(m, "ensuring %s %s", t.GVK.Kind, obj.GetName())

	if err != nil {
		t.teardown()
		t.unwatchDebugger(obj)
	} else {
		if obj.GetResourceVersion() != "" {
			t.Await(ctx, match.ResourceVersionAtLeast(obj.GetResourceVersion()))
		}
		DeferCleanup(func(_ context.Context) {
			t.teardown()
			t.unwatchDebugger(obj)
			t.checkFailed()
		})
	}
}

// CreateOrGet ensures the object exists, expecting success.
func (t *TrackedObject[T]) CreateOrGet(ctx context.Context) {
	GinkgoHelper()
	t.CreateOrGetExpect(ctx, Succeed())
}

// GetExpect wraps an already-existing object on the cluster into this
// TrackedObject, registering informers and debugger for full tracking.
// The get result is asserted against m. On success DeferCleanup removes
// informers (but does NOT delete — the test does not own the object).
// On expected failure informers are torn down immediately.
//
// No-op when Client is nil (unit-test mode).
func (t *TrackedObject[T]) GetExpect(ctx context.Context, m types.GomegaMatcher) {
	GinkgoHelper()
	if t.Client == nil {
		return
	}

	obj := t.lifecycle.OnNewEmpty()
	obj.SetName(t.objName)
	obj.GetObjectKind().SetGroupVersionKind(t.GVK)

	if t.lifecycle.Debugger != nil {
		Expect(t.lifecycle.Debugger.WatchRelated(obj)).To(Succeed(),
			"watch related for %s %s", t.GVK.Kind, obj.GetName())
	}

	t.setup(ctx)

	fmt.Fprintf(GinkgoWriter, "[%s] getting %s %s\n",
		time.Now().Format("15:04:05.000"), t.GVK.Kind, t.objName)

	err := t.Client.Get(ctx, client.ObjectKey{Name: t.objName}, obj)
	Expect(err).To(m, "getting %s %s", t.GVK.Kind, t.objName)

	if err != nil {
		t.teardown()
		t.unwatchDebugger(obj)
	} else {
		DeferCleanup(func(_ context.Context) {
			t.teardown()
			t.unwatchDebugger(obj)
			t.checkFailed()
		})
	}
}

// Get wraps an already-existing object, expecting it to exist.
func (t *TrackedObject[T]) Get(ctx context.Context) {
	GinkgoHelper()
	t.GetExpect(ctx, Succeed())
}

// UpdateExpect takes the latest snapshot, applies mutate to a copy,
// sends a merge-patch with the diff, and asserts the result against m.
// The base object comes from Object() (informer snapshot), not from an
// API-server Get — merge-patch only sends changed fields, so staleness
// of the base does not cause conflicts.
//
// Fails (via Object) if the object has not been observed yet or is deleted.
// No-op when Client is nil (unit-test mode).
func (t *TrackedObject[T]) UpdateExpect(ctx context.Context, mutate func(T), m types.GomegaMatcher) {
	GinkgoHelper()
	if t.Client == nil {
		return
	}

	original := t.Object()
	obj := original.DeepCopyObject().(T)
	mutate(obj)
	obj.GetObjectKind().SetGroupVersionKind(t.GVK)

	fmt.Fprintf(GinkgoWriter, "[%s] updating %s %s\n",
		time.Now().Format("15:04:05.000"), t.GVK.Kind, t.objName)

	err := t.Client.Patch(ctx, obj, client.MergeFrom(original))
	Expect(err).To(m, "updating %s %s", t.GVK.Kind, t.objName)
	if err == nil {
		t.Await(ctx, match.ResourceVersionAtLeast(obj.GetResourceVersion()))
	}
}

// Update fetches the latest object, applies mutate, and sends a
// merge-patch, expecting success.
func (t *TrackedObject[T]) Update(ctx context.Context, mutate func(T)) {
	GinkgoHelper()
	t.UpdateExpect(ctx, mutate, Succeed())
}

// AddLabel adds or overwrites a single label on the tracked object.
func (t *TrackedObject[T]) AddLabel(ctx context.Context, key, value string) {
	GinkgoHelper()
	t.Update(ctx, func(obj T) {
		labels := obj.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[key] = value
		obj.SetLabels(labels)
	})
}

// RemoveLabel removes a label by key from the tracked object.
// No-op if the label does not exist.
func (t *TrackedObject[T]) RemoveLabel(ctx context.Context, key string) {
	GinkgoHelper()
	t.Update(ctx, func(obj T) {
		labels := obj.GetLabels()
		if labels == nil {
			return
		}
		delete(labels, key)
		obj.SetLabels(labels)
	})
}

// AddAnnotation adds or overwrites a single annotation on the tracked object.
func (t *TrackedObject[T]) AddAnnotation(ctx context.Context, key, value string) {
	GinkgoHelper()
	t.Update(ctx, func(obj T) {
		annotations := obj.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[key] = value
		obj.SetAnnotations(annotations)
	})
}

// RemoveAnnotation removes an annotation by key from the tracked object.
// No-op if the annotation does not exist.
func (t *TrackedObject[T]) RemoveAnnotation(ctx context.Context, key string) {
	GinkgoHelper()
	t.Update(ctx, func(obj T) {
		annotations := obj.GetAnnotations()
		if annotations == nil {
			return
		}
		delete(annotations, key)
		obj.SetAnnotations(annotations)
	})
}

// DeleteExpect sends a delete request for the tracked object and
// asserts the result against m. On success, waits for the informer to
// acknowledge the delete (either a new resourceVersion or a DELETED
// event). Use Await(ctx, Deleted()) after Delete to wait for full
// deletion (all finalizers removed).
//
// No-op when Client is nil (unit-test mode).
func (t *TrackedObject[T]) DeleteExpect(ctx context.Context, m types.GomegaMatcher) {
	GinkgoHelper()
	if t.Client == nil {
		return
	}
	obj := t.Object()
	beforeRV := mustParseRV(obj.GetResourceVersion())
	fmt.Fprintf(GinkgoWriter, "[%s] deleting %s %s\n",
		time.Now().Format("15:04:05.000"), t.GVK.Kind, obj.GetName())
	err := client.IgnoreNotFound(t.Client.Delete(ctx, obj))
	Expect(err).To(m, "deleting %s %s", t.GVK.Kind, obj.GetName())
	if err == nil {
		t.awaitDeleteSync(ctx, beforeRV)
	}
}

// Delete sends a delete request for the tracked object, expecting success.
func (t *TrackedObject[T]) Delete(ctx context.Context) {
	GinkgoHelper()
	t.DeleteExpect(ctx, Succeed())
}

// deleteByName sends a best-effort delete request using only the object
// name and GVK — no dependency on Object() or snapshots. Used in
// DeferCleanup where the informer may not have delivered the first event.
func (t *TrackedObject[T]) deleteByName(ctx context.Context) {
	if t.Client == nil || t.lifecycle.OnNewEmpty == nil {
		return
	}
	obj := t.lifecycle.OnNewEmpty()
	obj.SetName(t.objName)
	obj.GetObjectKind().SetGroupVersionKind(t.GVK)
	_ = client.IgnoreNotFound(t.Client.Delete(ctx, obj))
}

// awaitDeleteSync waits for the informer to acknowledge a delete: either
// the object's resourceVersion advances beyond beforeRV (MODIFIED with
// deletionTimestamp) or the object is deleted (DELETED event). Unlike
// Await, this tolerates deletion (no fail-fast).
func (t *TrackedObject[T]) awaitDeleteSync(ctx context.Context, beforeRV int64) {
	GinkgoHelper()
	t.checkFailed()
	for {
		t.mu.RLock()
		if t.deleted {
			t.mu.RUnlock()
			break
		}
		if len(t.snapshots) > 0 {
			rv := mustParseRV(t.snapshots[len(t.snapshots)-1].Object.GetResourceVersion())
			if rv > beforeRV {
				t.mu.RUnlock()
				break
			}
		}
		ch := t.notify
		failed := t.failedCh
		t.mu.RUnlock()

		select {
		case <-ch:
		case <-failed:
			t.checkFailed()
			return
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				Fail(fmt.Sprintf("%s %s: timed out waiting for informer to acknowledge delete",
					t.GVK.Kind, t.objName))
			}
			return
		}
	}
	t.checkFailed()
}
