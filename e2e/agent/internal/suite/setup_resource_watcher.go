package suite

import (
	"context"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/utils"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// ResourceWatcher watches a single k8s resource via the Kubernetes watch API
// and notifies registered handlers when the resource changes.
type ResourceWatcher struct {
	*utils.EventDispatcher[watch.Event]
}

// SetupResourceWatcher starts a background watch on a k8s resource identified
// by key and returns an EventSource for watch events. The watch uses the
// Kubernetes watch API. If the watch fails to start or is unexpectedly closed
// by the server, the test is failed immediately.
//
// A watch-capable client is created internally using the same scheme as cl.
//
// newList is a factory that returns a new zero-value ObjectList for the watched
// resource type (e.g., func() client.ObjectList { return &v1alpha1.DRBDResourceList{} }).
// The list type determines which API endpoint is watched.
//
// The watcher is stopped during cleanup.
func SetupResourceWatcher(
	e *etesting.E,
	cl client.Client,
	key client.ObjectKey,
	newList func() client.ObjectList,
) utils.EventSource[watch.Event] {
	m := ResourceWatcher{
		EventDispatcher: utils.NewEventDispatcher(
			func(event watch.Event) watch.Event {
				return *event.DeepCopy()
			},
		),
	}

	kubeConfig, err := config.GetConfig()
	if err != nil {
		e.Fatalf("resource monitor: getting kubeconfig: %v", err)
	}

	watchClient, err := client.NewWithWatch(kubeConfig, client.Options{
		Scheme: cl.Scheme(),
	})
	if err != nil {
		e.Fatalf("resource monitor: creating watch client: %v", err)
	}

	ctx, cancel := context.WithCancel(e.Context())
	wg := &sync.WaitGroup{}

	wg.Go(func() {
		m.runWatch(ctx, e, watchClient, key, newList)
	})

	e.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	return m
}

func (m *ResourceWatcher) runWatch(
	ctx context.Context,
	e *etesting.E,
	watchClient client.WithWatch,
	key client.ObjectKey,
	newList func() client.ObjectList,
) {
	listOpts := &client.ListOptions{
		Namespace:     key.Namespace,
		FieldSelector: fields.OneTermEqualSelector("metadata.name", key.Name),
	}

	watcher, err := watchClient.Watch(ctx, newList(), listOpts)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		e.Fatalf("resource monitor: error starting watch for %s: %v", key, err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				e.Fatalf("resource monitor: watch for '%s' unexpectedly closed", key)
			} else {
				m.DispatchEvent(event)
			}
		}
	}
}
