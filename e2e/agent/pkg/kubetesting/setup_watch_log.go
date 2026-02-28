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
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// SetupWatchLog wraps a watch event channel, logging every event to e.Logf
// and forwarding it to the returned channel. The returned channel closes when
// the input channel closes.
//
// Logging behavior by event type:
//   - Added: event type, name, full object state (JSON)
//   - Modified: event type, name, diff against last known state
//   - Deleted: event type, name
//   - Other: event type, and %s of Object if present
func SetupWatchLog(e envtesting.E, ch <-chan watch.Event) <-chan watch.Event {
	out := make(chan watch.Event)
	ctx := e.Context()

	wg := sync.WaitGroup{}
	wg.Go(func() {
		defer close(out)
		var lastKnown runtime.Object
		for event := range ch {
			if event.Type == watch.Error && ctx.Err() != nil {
				continue
			}
			logWatchEvent(e, event, lastKnown)
			if event.Object != nil {
				lastKnown = event.Object.DeepCopyObject()
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	})

	e.Cleanup(wg.Wait)

	return out
}

func logWatchEvent(e envtesting.E, event watch.Event, lastKnown runtime.Object) {
	e.Logf("watch: %s %s", event.Type, objectName(event.Object))

	switch {
	case event.Type == watch.Modified && lastKnown != nil:
		e.Log(cmp.Diff(lastKnown, event.Object))
	case event.Type == watch.Deleted:
	case event.Object != nil:
		e.Log(formatObject(event.Object))
	}
}

func objectName(obj runtime.Object) string {
	if obj == nil {
		return "<nil>"
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return "<unknown>"
	}
	return accessor.GetName()
}

func formatObject(obj runtime.Object) string {
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", obj)
	}
	return string(data)
}
