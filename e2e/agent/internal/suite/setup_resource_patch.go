package suite

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// SetupResourcePatch fetches the resource identified by key, applies mutate as
// a merge patch, and optionally waits for it to reach a desired state.
//
// If predicate is non-nil, a watcher is started before patching, the function
// waits until predicate returns true, then the watcher is cleaned up. The
// returned object is the one that matched. The timeout comes from e.Context()
// (caller should use e.ScopeWithTimeout if a timeout is needed).
//
// If predicate is nil, the resource is patched and the patched object is returned.
//
// The reverse patch is pre-computed immediately after mutate, so the revert
// cleanup is exact regardless of what happens to the resource later.
func SetupResourcePatch[T any, PT objectPtr[T]](
	e envtesting.E,
	wc client.WithWatch,
	key client.ObjectKey,
	mutate func(PT),
	predicate func(PT) bool,
) PT {
	obj := PT(new(T))
	kind := fmt.Sprintf("%T", obj)

	if predicate == nil {
		patchResource(e, wc, key, obj, kind, mutate)
		return obj
	}

	watcherScope := e.Scope()
	defer watcherScope.Close()

	logCh := startWatcher(watcherScope, wc, obj, key, kind)
	patchResource(e, wc, key, obj, kind, mutate)
	return waitForMatch(e, logCh, key, kind, predicate)
}

func patchResource[T any, PT objectPtr[T]](
	e envtesting.E,
	cl client.Client,
	key client.ObjectKey,
	obj PT,
	kind string,
	mutate func(PT),
) {
	if err := cl.Get(e.Context(), key, obj); err != nil {
		e.Fatalf("getting %s %q before patch: %v", kind, key, err)
	}

	original := obj.DeepCopyObject().(client.Object)
	mutate(obj)

	revertPatch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	revertBytes, err := revertPatch.Data(original)
	if err != nil {
		e.Fatalf("computing revert patch for %s %q: %v", kind, key, err)
	}

	if err := cl.Patch(e.Context(), obj, client.MergeFrom(original)); err != nil {
		e.Fatalf("patching %s %q: %v", kind, key, err)
	}

	e.Cleanup(func() {
		ctx := context.Background()
		fresh := PT(new(T))

		if err := cl.Get(ctx, key, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: getting %s %q for revert: %v", kind, key, err)
			return
		}

		if err := cl.Patch(ctx, fresh,
			client.RawPatch(types.MergePatchType, revertBytes)); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: reverting patch on %s %q: %v", kind, key, err)
		}
	})
}
