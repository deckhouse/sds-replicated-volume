package suite

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	capi "github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	csync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
)

// objectPtr constrains PT to be a pointer to T that implements client.Object.
type objectPtr[T any] interface {
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
func SetupResource[T any, PT objectPtr[T]](
	e envtesting.E,
	wc client.WithWatch,
	obj PT,
	predicate func(PT) bool,
) PT {
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if predicate == nil {
		createResource(e, wc, obj, key, kind)
		return obj
	}

	watcherScope := e.Scope()
	defer watcherScope.Close()

	logCh := startWatcher(watcherScope, wc, obj, key, kind)
	createResource(e, wc, obj, key, kind)
	return waitForMatch(e, logCh, key, kind, predicate)
}

func createResource(e envtesting.E, cl client.Client, obj client.Object, key client.ObjectKey, kind string) {
	if err := cl.Create(e.Context(), obj); err != nil {
		e.Fatalf("creating %s %q: %v", kind, key, err)
	}
	e.Cleanup(func() { cleanupResource(e, cl, obj) })
}

func startWatcher(
	e envtesting.E,
	wc client.WithWatch,
	obj client.Object,
	key client.ObjectKey,
	kind string,
) <-chan watch.Event {
	list, err := capi.NewListForObject(obj, wc.Scheme())
	if err != nil {
		e.Fatalf("creating list type for %s: %v", kind, err)
	}

	watcher, err := wc.Watch(e.Context(), list, &client.ListOptions{
		Namespace:     key.Namespace,
		FieldSelector: fields.OneTermEqualSelector("metadata.name", key.Name),
	})
	if err != nil {
		e.Fatalf("setting up watcher for %s %q: %v", kind, key, err)
	}
	e.Cleanup(watcher.Stop)

	return setupWatchLogInternal(e, watcher.ResultChan())
}

func waitForMatch[T any, PT objectPtr[T]](
	e envtesting.E,
	logCh <-chan watch.Event,
	key client.ObjectKey,
	kind string,
	predicate func(PT) bool,
) PT {
	event, ok := csync.Wait(logCh, func(ev watch.Event) bool {
		return predicate(ev.Object.(PT))
	})
	if !ok {
		e.Fatalf("waiting for %s %q: timed out or context cancelled", kind, key)
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
