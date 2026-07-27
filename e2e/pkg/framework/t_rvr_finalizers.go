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

package framework

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// rvrFinalizerRemovalPatch drops every finalizer of a replica in one write.
var rvrFinalizerRemovalPatch = []byte(`{"metadata":{"finalizers":null}}`)

// RemoveFinalizers strips every finalizer from the replica with a direct merge
// patch, going around the controller that owns them.
//
// THIS IS AN ESCAPE HATCH, NOT A WAIT HELPER. Removing a finalizer by hand
// hides cleanup-path bugs, so a spec may only use it when the deadlock it
// escapes is by design and the manual escape is the very thing under test (the
// operator recipe from debug_and_problem_solving.md). Such a spec MUST carry
// LabelDisruptive and MUST be listed among the deliberate exceptions in
// e2e/full/RUNNING.md. Anywhere else a finalizer that does not go away on its
// own is a bug to report, not to patch out.
//
// The tracked Update helper cannot do this: dropping the last finalizer of an
// object that already has a deletion timestamp makes it vanish, and Update
// would then wait for a resourceVersion nobody will ever publish. A replica
// that is already gone is the intended outcome, so NotFound is success.
func (t *TestRVR) RemoveFinalizers(ctx context.Context) {
	GinkgoHelper()
	if err := removeRVRFinalizers(ctx, t.Client, t.Name()); err != nil {
		Fail(err.Error())
	}
}

// objectPatcher is the seam the patch cores write through. client.Client
// implements it against the cluster; unit tests substitute a stub.
type objectPatcher interface {
	Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error
}

// removeRVRFinalizers is the failing logic of RemoveFinalizers.
func removeRVRFinalizers(ctx context.Context, patcher objectPatcher, name string) error {
	rvr := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: name}}
	patch := client.RawPatch(types.MergePatchType, rvrFinalizerRemovalPatch)
	if err := client.IgnoreNotFound(patcher.Patch(ctx, rvr, patch)); err != nil {
		return fmt.Errorf("removing the finalizers of %s: %w", name, err)
	}
	return nil
}
