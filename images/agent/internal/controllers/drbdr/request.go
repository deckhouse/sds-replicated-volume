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

package drbdr

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/event"
)

// DRBDReconcileRequest represents a reconciliation request for a DRBD resource.
// Exactly one of Name or ActualNameOnTheNode should be set:
//   - Name: set for K8S-originated events (watch on DRBDResource) and scanner events for prefixed DRBD names
//   - ActualNameOnTheNode: set for scanner-originated events for non-prefixed DRBD names (orphan/rename handling)
type DRBDReconcileRequest struct {
	// Name is the K8S resource name. Set for K8S-originated events and scanner events
	// where the DRBD name has the standard "sdsrv-" prefix.
	Name string
	// ActualNameOnTheNode is the DRBD resource name as observed on the node.
	// Set for scanner-originated events where the DRBD name does not have the standard prefix.
	ActualNameOnTheNode string
}

// requestChBuffer bounds the pending wake-ups. A node can hold far more
// DRBDResources than this, so senders block rather than drop: a dropped wake-up for
// a resource with nothing else queued would leave it unreconciled.
const requestChBuffer = 1000

// requestCh carries wake-ups into the DRBDResource controller from its scanner.
var requestCh = make(chan event.TypedGenericEvent[DRBDReconcileRequest], requestChBuffer)

// enqueueReconcile asks the DRBDResource controller to reconcile the DRBDResource
// with the given name. It blocks while the queue is full, and returns early when ctx
// ends.
func enqueueReconcile(ctx context.Context, name string) {
	enqueue(ctx, DRBDReconcileRequest{Name: name})
}

// enqueueReconcileByActualName asks the DRBDResource controller to reconcile the DRBD
// resource observed on the node under a name that carries no standard prefix, for
// orphan and rename handling. It blocks while the queue is full, and returns early
// when ctx ends.
func enqueueReconcileByActualName(ctx context.Context, actualNameOnTheNode string) {
	enqueue(ctx, DRBDReconcileRequest{ActualNameOnTheNode: actualNameOnTheNode})
}

func enqueue(ctx context.Context, req DRBDReconcileRequest) {
	select {
	case requestCh <- event.TypedGenericEvent[DRBDReconcileRequest]{Object: req}:
	case <-ctx.Done():
	}
}
