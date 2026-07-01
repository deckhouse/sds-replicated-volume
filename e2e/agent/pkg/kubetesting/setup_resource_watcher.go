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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	capi "github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	csync "github.com/deckhouse/sds-replicated-volume/lib/go/common/sync"
)

// SetupResourceWatcher starts a watch on a k8s resource and returns a wait
// function. The list type is derived automatically from obj. The watch
// lifecycle is bound to e.Context(); the watcher is stopped during cleanup.
// Events are logged via SetupWatchLog.
//
// The returned function blocks until predicate returns true for an event whose
// object has the same UID and generation >= the passed obj. It fails the test
// if e.Context() is cancelled or the channel closes without a match.
func SetupResourceWatcher[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	wc client.WithWatch,
	obj PT,
) func(e envtesting.E, obj PT, predicate func(PT) bool) PT {
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

	return func(e envtesting.E, obj PT, predicate func(PT) bool) PT {
		e.Helper()
		uid := obj.GetUID()
		minGeneration := obj.GetGeneration()
		event, ok := csync.Wait(e.Context(), ch, func(ev watch.Event) bool {
			o := ev.Object.(PT)
			if o.GetUID() != uid {
				return false
			}
			if o.GetGeneration() < minGeneration {
				return false
			}
			return predicate(o)
		})
		if !ok {
			e.Fatalf("waiting for match: %v", e.Context().Err())
		}
		return event.Object.(PT)
	}
}
