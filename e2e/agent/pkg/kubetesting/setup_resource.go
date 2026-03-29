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

// SetupResourceOption configures SetupResource behavior.
type SetupResourceOption func(*setupResourceConfig)

type setupResourceConfig struct {
	reuseIfFound bool
}

// ReuseIfFound makes SetupResource try to discover an existing resource by
// name first. If found, the predicate is checked immediately (no waiting) and
// no cleanup is registered. If the resource does not exist, SetupResource
// falls back to normal create-and-wait behavior.
func ReuseIfFound() SetupResourceOption {
	return func(c *setupResourceConfig) {
		c.reuseIfFound = true
	}
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
//
// With the Reuse option, the resource is fetched by name instead of created,
// the predicate is checked immediately without waiting, and no cleanup is
// registered.
func SetupResource[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	wc client.WithWatch,
	obj PT,
	predicate func(PT) bool,
	opts ...SetupResourceOption,
) PT {
	e.Helper()

	var cfg setupResourceConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.reuseIfFound {
		reused, ok := tryReuseResource(e, wc, obj, predicate)
		if ok {
			return reused
		}
	}

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

// tryReuseResource attempts to fetch an existing resource by name. If found,
// it checks the predicate immediately and returns (obj, true). If the resource
// does not exist, it returns (zero, false) so the caller can fall back to
// normal creation.
func tryReuseResource[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	cl client.Client,
	obj PT,
	predicate func(PT) bool,
) (PT, bool) {
	e.Helper()
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Get(e.Context(), key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			e.Logf("%s %q not found, creating normally", kind, key)
			return obj, false
		}
		e.Fatalf("reusing %s %q: %v", kind, key, err)
	}
	e.Logf("reusing existing %s %q", kind, key)

	if predicate != nil && !predicate(obj) {
		e.Fatalf("reusing %s %q: predicate not satisfied", kind, key)
	}

	return obj, true
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
