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

package kubetesting

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
func SetupResourcePatch[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	wc client.WithWatch,
	key client.ObjectKey,
	mutate func(PT),
	predicate func(PT) bool,
) PT {
	e.Helper()

	obj := PT(new(T))
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)

	if predicate == nil {
		patchResource(e, wc, obj, mutate)
		return obj
	}

	watcherScope := e.Scope()
	defer watcherScope.Close()

	wait := SetupResourceWatcher(watcherScope, wc, obj)
	patchResource(e, wc, obj, mutate)

	return wait(e, obj, predicate)
}

func patchResource[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	cl client.Client,
	obj PT,
	mutate func(PT),
) {
	e.Helper()
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Get(e.Context(), key, obj); err != nil {
		e.Fatalf("getting %s %q before patch: %v", kind, key, err)
	}

	original := obj.DeepCopyObject().(client.Object)
	mutate(obj)

	revertFrom := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	revertBytes, err := revertFrom.Data(original)
	if err != nil {
		e.Fatalf("computing revert patch: %v", err)
	}

	if err := cl.Patch(e.Context(), obj, client.MergeFrom(original)); err != nil {
		e.Fatalf("patching %s %q: %v", kind, key, err)
	}

	e.Cleanup(func() { applyRevertPatch(e, cl, obj, revertBytes) })
}

func applyRevertPatch[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	cl client.Client,
	obj PT,
	revertBytes []byte,
) {
	ctx := context.Background()
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

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
}
