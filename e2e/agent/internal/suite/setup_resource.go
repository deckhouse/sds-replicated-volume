package suite

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	capi "github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	csync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
)

// clientObjectAndPtr constrains PT to be a pointer to T that implements client.Object.
type clientObjectAndPtr[T any] interface {
	client.Object
	*T
}

// SetupResource creates the given resource and optionally waits for it to
// reach a desired state.
//
// If predicate is non-nil, a watcher is started before creation, the function
// waits until predicate returns true, then the watcher is cleaned up. The
// returned object is the one that matched. The timeout comes from e.Context()
// (caller should use e.ScopeWithTimeout if a timeout is needed).
//
// If predicate is nil, the resource is created and returned immediately.
//
// Resource cleanup (finalizer removal and deletion) is registered on e.
func SetupResource[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	wc client.WithWatch,
	obj PT,
	predicate func(PT) bool,
) PT {
	e.Helper()

	if predicate == nil {
		createResource(e, wc, obj)
		return obj
	}

	watcherScope := e.Scope()
	defer watcherScope.Close()

	ch := startWatcher(watcherScope, wc, obj)
	createResource(e, wc, obj)

	return waitForMatch(e, ch, obj.GetUID(), obj.GetGeneration(), predicate)
}

func createResource(e envtesting.E, cl client.Client, obj client.Object) {
	e.Helper()
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Create(e.Context(), obj); err != nil {
		e.Fatalf("creating %s %q: %v", kind, key, err)
	}
	e.Cleanup(func() { cleanupResource(e, cl, obj) })
}

func startWatcher(
	e envtesting.E,
	wc client.WithWatch,
	obj client.Object,
) <-chan watch.Event {
	e.Helper()

	list, err := capi.NewListForObject(obj, wc.Scheme())
	if err != nil {
		e.Fatalf("creating list type: %v", err)
	}

	watcher, err := wc.Watch(e.Context(), list, &client.ListOptions{
		Namespace:     obj.GetNamespace(),
		FieldSelector: fields.OneTermEqualSelector("metadata.name", obj.GetName()),
	})
	if err != nil {
		e.Fatalf("setting up watcher: %v", err)
	}

	ch := SetupWatchLog(e, watcher.ResultChan())
	e.Cleanup(watcher.Stop)

	return ch
}

func waitForMatch[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	logCh <-chan watch.Event,
	uid types.UID,
	minGeneration int64,
	predicate func(PT) bool,
) PT {
	e.Helper()
	event, ok := csync.Wait(logCh, func(ev watch.Event) bool {
		obj := ev.Object.(PT)
		if obj.GetUID() != uid {
			return false
		}
		if obj.GetGeneration() < minGeneration {
			return false
		}
		return predicate(obj)
	})
	if !ok {
		e.Fatalf("waiting failed")
	}
	return event.Object.(PT)
}

// cleanupResource removes all finalizers from the resource and deletes it.
func cleanupResource(e envtesting.E, cl client.Client, obj client.Object) {
	ctx := context.Background()
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: getting %s %q: %v", kind, key, err)
		return
	}

	if len(obj.GetFinalizers()) > 0 {
		patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
		obj.SetFinalizers(nil)
		if err := cl.Patch(ctx, obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: removing finalizers from %s %q: %v", kind, key, err)
			return
		}
	}

	if err := cl.Delete(ctx, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: deleting %s %q: %v", kind, key, err)
	}
}
