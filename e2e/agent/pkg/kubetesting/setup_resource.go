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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
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
// Resource cleanup (deletion + wait for disappearance) is registered on e.
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

	wait := SetupResourceWatcher(watcherScope, wc, obj)
	createResource(e, wc, obj)

	return wait(e, obj, predicate)
}

const deletionTimeout = 5 * time.Second

// createResource creates the resource and registers a Delete cleanup on e.
// Cleanup deletes the resource and waits up to deletionTimeout for it to
// fully disappear (controllers may hold finalizers).
func createResource(e envtesting.E, cl client.Client, obj client.Object) {
	e.Helper()
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Create(e.Context(), obj); err != nil {
		e.Fatalf("creating %s %q: %v", kind, key, err)
	}
	e.Cleanup(func() { cleanupResource(e, cl, obj) })
}

func cleanupResource(e envtesting.E, cl client.Client, obj client.Object) {
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Delete(context.Background(), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: deleting %s %q: %v", kind, key, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), deletionTimeout)
	defer cancel()

	probe := obj.DeepCopyObject().(client.Object)
	for {
		select {
		case <-ctx.Done():
			e.Errorf("cleanup: timed out waiting for %s %q to be deleted", kind, key)
			return
		case <-time.After(50 * time.Millisecond):
		}
		if err := cl.Get(ctx, key, probe); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: waiting for deletion of %s %q: %v", kind, key, err)
			return
		}
	}
}
