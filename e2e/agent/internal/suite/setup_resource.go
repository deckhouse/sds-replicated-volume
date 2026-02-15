package suite

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupResource creates the given resource and registers a reliable cleanup
// that removes all finalizers and deletes the resource after the test.
//
// The resource object must have Name (and optionally Namespace) set.
// If the resource already exists, the function fatals.
//
// Cleanup sequence:
//  1. Re-fetch the resource (skip if already gone).
//  2. Remove all finalizers via patch.
//  3. Delete the resource (skip if already gone).
func SetupResource(
	e *etesting.E,
	cl client.Client,
	obj client.Object,
) {
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	if err := cl.Create(e.Context(), obj); err != nil {
		e.Fatalf("creating %s %q: %v", kind, key, err)
	}

	e.Cleanup(func() {
		cleanupResource(e, cl, obj)
	})
}

// cleanupResource removes all finalizers from the resource and deletes it.
// Errors are reported via e.Errorf so that cleanup continues for other
// resources even if one fails.
func cleanupResource(e *etesting.E, cl client.Client, obj client.Object) {
	key := client.ObjectKeyFromObject(obj)
	kind := fmt.Sprintf("%T", obj)

	// Re-fetch to get the latest version.
	if err := cl.Get(e.Context(), key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: getting %s %q: %v", kind, key, err)
		return
	}

	// Remove all finalizers if any are present.
	if len(obj.GetFinalizers()) > 0 {
		patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
		obj.SetFinalizers(nil)
		if err := cl.Patch(e.Context(), obj, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: removing finalizers from %s %q: %v", kind, key, err)
			return
		}
	}

	// Delete.
	if err := cl.Delete(e.Context(), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		e.Errorf("cleanup: deleting %s %q: %v", kind, key, err)
	}
}
