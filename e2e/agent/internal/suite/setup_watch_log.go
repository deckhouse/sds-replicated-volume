package suite

import (
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/watch"
)

// SetupWatchLog wraps a watch event channel, logging every event to e.Logf
// and forwarding it to the returned channel. The returned channel closes when
// the input channel closes.
func SetupWatchLog(e *envtesting.E, ch <-chan watch.Event) <-chan watch.Event {
	out := make(chan watch.Event)
	go func() {
		defer close(out)
		for event := range ch {
			accessor, err := meta.Accessor(event.Object)
			if err != nil {
				e.Logf("watch: %s <unknown>", event.Type)
			} else {
				e.Logf("watch: %s %s", event.Type, accessor.GetName())
			}
			out <- event
		}
	}()
	return out
}
