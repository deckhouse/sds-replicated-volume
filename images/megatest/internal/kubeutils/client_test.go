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

package kubeutils

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestDispatchRVEventDoesNotDropWhenChannelIsFull(t *testing.T) {
	rvName := "rv-1"
	ch := make(chan *v1alpha1.ReplicatedVolume, 1)
	done := make(chan struct{})
	client := &Client{
		informerStop: make(chan struct{}),
		rvCheckers: map[string]rvCheckerRegistration{
			rvName: {
				ch:   ch,
				done: done,
			},
		},
	}

	first := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: rvName, ResourceVersion: "1"}}
	second := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: rvName, ResourceVersion: "2"}}

	client.dispatchRVEvent(first)

	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		client.dispatchRVEvent(second)
	}()

	select {
	case <-dispatched:
		t.Fatal("dispatch should block while the checker channel is full")
	case <-time.After(20 * time.Millisecond):
	}

	if got := <-ch; got.ResourceVersion != "1" {
		t.Fatalf("expected first event to be preserved, got resourceVersion=%q", got.ResourceVersion)
	}

	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("dispatch did not complete after the checker channel was drained")
	}

	if got := <-ch; got.ResourceVersion != "2" {
		t.Fatalf("expected second event after drain, got resourceVersion=%q", got.ResourceVersion)
	}
}

func TestDispatchRVEventUnblocksWhenCheckerStops(t *testing.T) {
	rvName := "rv-1"
	ch := make(chan *v1alpha1.ReplicatedVolume, 1)
	done := make(chan struct{})
	client := &Client{
		informerStop: make(chan struct{}),
		rvCheckers: map[string]rvCheckerRegistration{
			rvName: {
				ch:   ch,
				done: done,
			},
		},
	}

	first := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: rvName, ResourceVersion: "1"}}
	second := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: rvName, ResourceVersion: "2"}}

	client.dispatchRVEvent(first)

	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		client.dispatchRVEvent(second)
	}()

	select {
	case <-dispatched:
		t.Fatal("dispatch should block while the checker channel is full")
	case <-time.After(20 * time.Millisecond):
	}

	close(done)

	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("dispatch did not unblock after checker stopped")
	}

	if got := <-ch; got.ResourceVersion != "1" {
		t.Fatalf("expected first event to be preserved, got resourceVersion=%q", got.ResourceVersion)
	}
}
