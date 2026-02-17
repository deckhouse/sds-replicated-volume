package suite

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
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
func SetupWatchLog(e *envtesting.E, ch <-chan watch.Event) <-chan watch.Event {
	out := make(chan watch.Event)
	wg := sync.WaitGroup{}
	wg.Go(func() {
		defer close(out)
		var lastKnown runtime.Object
		for event := range ch {
			logWatchEvent(e, event, lastKnown)
			if event.Object != nil {
				lastKnown = event.Object.DeepCopyObject()
			}
			out <- event
		}
	})
	e.Cleanup(func() {
		wg.Wait()
	})
	return out
}

func logWatchEvent(e *envtesting.E, event watch.Event, lastKnown runtime.Object) {
	name := objectName(event.Object)

	switch event.Type {
	case watch.Added:
		e.Logf("watch: %s %s\n%s", event.Type, name, formatObject(event.Object))
	case watch.Modified:
		if lastKnown != nil {
			e.Logf("watch: %s %s\n%s", event.Type, name, cmp.Diff(lastKnown, event.Object))
		} else {
			e.Logf("watch: %s %s\n%s", event.Type, name, formatObject(event.Object))
		}
	case watch.Deleted:
		e.Logf("watch: %s %s", event.Type, name)
	default:
		if event.Object != nil {
			e.Logf("watch: %s %s", event.Type, fmt.Sprintf("%s", event.Object))
		} else {
			e.Logf("watch: %s", event.Type)
		}
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
