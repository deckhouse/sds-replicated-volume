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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	capi "github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

// DiscoverResources lists resources of type T by name and returns them in the
// same order as names. Each name must exist exactly once. If predicate is
// non-nil, only items for which it returns true are considered matches.
// Fails the test if any name is missing or duplicated.
func DiscoverResources[T any, PT clientObjectAndPtr[T]](
	e envtesting.E,
	cl client.Client,
	names []string,
	predicate func(PT) bool,
) []PT {
	e.Helper()

	if len(names) == 0 {
		e.Fatalf("DiscoverResources: names must be non-empty")
	}

	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	if len(nameSet) != len(names) {
		e.Fatalf("DiscoverResources: names contain duplicates: %v", names)
	}

	obj := PT(new(T))
	list, err := capi.NewListForObject(obj, cl.Scheme())
	if err != nil {
		e.Fatalf("creating list type for %T: %v", obj, err)
	}

	if err := cl.List(e.Context(), list); err != nil {
		e.Fatalf("listing %T: %v", list, err)
	}

	byName := make(map[string]PT, len(names))
	if err := meta.EachListItem(list, func(item runtime.Object) error {
		typed, ok := item.(PT)
		if !ok {
			return fmt.Errorf("unexpected item type %T", item)
		}
		if _, want := nameSet[typed.GetName()]; !want {
			return nil
		}
		if predicate != nil && !predicate(typed) {
			return nil
		}
		byName[typed.GetName()] = typed
		return nil
	}); err != nil {
		e.Fatalf("iterating list items: %v", err)
	}

	result := make([]PT, 0, len(names))
	var missing []string
	for _, n := range names {
		if obj, ok := byName[n]; ok {
			result = append(result, obj)
		} else {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		e.Fatalf("DiscoverResources: not found: %v", missing)
	}

	return result
}
