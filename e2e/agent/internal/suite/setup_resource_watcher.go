package suite

import (
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupResourceWatcher starts a watch on a k8s resource identified by key and
// returns the watcher's event channel. The watch lifecycle is bound to
// e.Context(); the watcher is stopped during cleanup.
//
// list is a zero-value ObjectList for the watched resource type
// (e.g., &v1alpha1.DRBDResourceList{}).
func SetupResourceWatcher(
	e envtesting.E,
	wc client.WithWatch,
	key client.ObjectKey,
	list client.ObjectList,
) <-chan watch.Event {
	listOpts := &client.ListOptions{
		Namespace:     key.Namespace,
		FieldSelector: fields.OneTermEqualSelector("metadata.name", key.Name),
	}

	watcher, err := wc.Watch(e.Context(), list, listOpts)
	if err != nil {
		e.Fatalf("resource watcher: starting watch for %s: %v", key, err)
	}

	e.Cleanup(func() {
		watcher.Stop()
	})

	return watcher.ResultChan()
}
