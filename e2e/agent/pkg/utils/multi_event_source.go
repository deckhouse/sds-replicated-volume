package utils

import (
	"context"
	"sync"
)

// KeyedEvent pairs an event value with the key of the source it came from.
type KeyedEvent[K comparable, V any] struct {
	Key   K
	Value V
}

// MultiEventSource joins multiple keyed EventSource[V] into a single
// EventSource[KeyedEvent[K, V]]. Child source subscriptions are managed lazily:
// the multi source subscribes to children when the first subscriber is added
// and unsubscribes when the last subscriber is removed.
type MultiEventSource[K comparable, V any] struct {
	mu              sync.Mutex
	sources         map[K]EventSource[V]
	dispatcher      *EventDispatcher[KeyedEvent[K, V]]
	subscriberCount int
	childCleanups   []func()
}

// NewMultiEventSource creates a new MultiEventSource from the given keyed
// sources.
func NewMultiEventSource[K comparable, V any](sources map[K]EventSource[V]) *MultiEventSource[K, V] {
	return &MultiEventSource[K, V]{
		sources:    sources,
		dispatcher: NewEventDispatcher[KeyedEvent[K, V]](nil),
	}
}

// SetupSubscriber registers a handler for keyed events. On the first
// subscriber, the multi source subscribes to all child sources. When the last
// subscriber's cleanup runs, child subscriptions are removed.
func (m *MultiEventSource[K, V]) SetupSubscriber(handler func(KeyedEvent[K, V])) func() {
	// Register handler first so it receives events as soon as children are
	// subscribed.
	dispatcherCleanup := m.dispatcher.SetupSubscriber(handler)

	m.mu.Lock()
	m.subscriberCount++
	if m.subscriberCount == 1 {
		m.subscribeToChildrenLocked()
	}
	m.mu.Unlock()

	return func() {
		m.mu.Lock()
		m.subscriberCount--
		if m.subscriberCount == 0 {
			m.unsubscribeFromChildrenLocked()
		}
		m.mu.Unlock()

		dispatcherCleanup()
	}
}

// Chan returns a channel that receives keyed events until ctx is cancelled.
func (m *MultiEventSource[K, V]) Chan(ctx context.Context) <-chan KeyedEvent[K, V] {
	ch := make(chan KeyedEvent[K, V])
	cleanup := m.SetupSubscriber(func(event KeyedEvent[K, V]) {
		ch <- event
	})
	go func() {
		<-ctx.Done()
		cleanup()
		close(ch)
	}()
	return ch
}

func (m *MultiEventSource[K, V]) subscribeToChildrenLocked() {
	for key, source := range m.sources {
		cleanup := source.SetupSubscriber(func(event V) {
			m.dispatcher.DispatchEvent(KeyedEvent[K, V]{Key: key, Value: event})
		})
		m.childCleanups = append(m.childCleanups, cleanup)
	}
}

func (m *MultiEventSource[K, V]) unsubscribeFromChildrenLocked() {
	for _, c := range m.childCleanups {
		c()
	}
	m.childCleanups = nil
}
