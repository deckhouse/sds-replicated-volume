package suite

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
	wg := sync.WaitGroup{}
	out := pipeWatchLog(e, ch, &wg)
	e.Cleanup(func() { wg.Wait() })
	return out
}

// setupWatchLogInternal is like SetupWatchLog but does NOT register cleanup.
// The caller is responsible for managing the watcher lifecycle (typically via
// a scoped context that cancels when the watcher should stop).
func setupWatchLogInternal(e envtesting.E, ch <-chan watch.Event) <-chan watch.Event {
	return pipeWatchLog(e, ch, nil)
}

// pipeWatchLog runs the log-and-forward goroutine. If wg is non-nil, the
// goroutine is tracked in it.
func pipeWatchLog(e envtesting.E, ch <-chan watch.Event, wg *sync.WaitGroup) <-chan watch.Event {
	out := make(chan watch.Event)
	ctx := e.Context()
	runGo := func(fn func()) { go fn() }
	if wg != nil {
		runGo = wg.Go
	}
	runGo(func() {
		defer close(out)
		var lastKnown runtime.Object
		for event := range ch {
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
	return out
}

func logWatchEvent(e envtesting.E, event watch.Event, lastKnown runtime.Object) {
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
