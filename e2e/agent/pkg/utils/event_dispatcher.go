package utils

import (
	"context"
	"slices"
	"sync"
)

// EventSource is a generic event pub/sub source. Use SetupSubscriber for
// callback-based consumption or Chan for channel-based consumption.
type EventSource[T any] interface {
	// SetupSubscriber registers a handler called for each event. Returns a
	// cleanup function that unregisters the handler.
	SetupSubscriber(handler func(T)) func()

	// Chan returns a channel that receives events until ctx is cancelled.
	// The subscription is cleaned up automatically when the context is done.
	Chan(ctx context.Context) <-chan T
}

// EventDispatcher is a generic, thread-safe pub/sub dispatcher. Each
// subscriber receives a deep-copied event produced by the clone function
// supplied at construction time.
type EventDispatcher[T any] struct {
	mu       sync.Mutex
	handlers []*func(T)
	clone    func(T) T
}

// NewEventDispatcher creates a new EventDispatcher. If clone is nil, events are
// passed to subscribers as-is (no copying).
func NewEventDispatcher[T any](clone func(T) T) *EventDispatcher[T] {
	if clone == nil {
		clone = func(v T) T { return v }
	}
	return &EventDispatcher[T]{
		clone: clone,
	}
}

// SetupSubscriber registers a handler that is called for each dispatched event.
// Returns a cleanup function that unregisters the handler.
func (d *EventDispatcher[T]) SetupSubscriber(handler func(T)) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers = append(d.handlers, &handler)
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.handlers = slices.DeleteFunc(
			d.handlers,
			func(el *func(T)) bool { return el == &handler },
		)
	}
}

// Chan returns a channel that receives events until ctx is cancelled. The
// channel is closed and the subscription cleaned up when the context is done.
func (d *EventDispatcher[T]) Chan(ctx context.Context) <-chan T {
	ch := make(chan T)
	cleanup := d.SetupSubscriber(func(event T) {
		ch <- event
	})
	go func() {
		<-ctx.Done()
		cleanup()
		close(ch)
	}()
	return ch
}

// DispatchEvent clones the event and delivers it to every registered handler.
// The lock is held for the entire dispatch so that cleanup functions returned
// by SetupSubscriber block until all in-flight handler calls complete.
// Handlers must not call SetupSubscriber or cleanup functions on the same
// dispatcher.
func (d *EventDispatcher[T]) DispatchEvent(event T) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, h := range d.handlers {
		(*h)(d.clone(event))
	}
}
