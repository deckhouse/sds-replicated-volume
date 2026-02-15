package suite

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupResourcePatch fetches the resource identified by key, applies mutate as
// a merge patch, and registers a cleanup that reverts the mutation.
//
// The resource is fetched internally — the caller only provides the key. The
// mutate callback receives the fetched object with its concrete type so the
// caller can modify any typed fields.
//
// The reverse patch is pre-computed immediately after mutate, so the revert is
// exact regardless of what happens to the resource between the patch and
// cleanup.
func SetupResourcePatch[T any, PT interface {
	client.Object
	*T
}](
	e *etesting.E,
	cl client.Client,
	key client.ObjectKey,
	mutate func(PT),
) {
	obj := PT(new(T))
	kind := fmt.Sprintf("%T", obj)

	// Fetch the latest state.
	if err := cl.Get(e.Context(), key, obj); err != nil {
		e.Fatalf("getting %s %q before patch: %v", kind, key, err)
	}

	// Snapshot before mutation.
	original := obj.DeepCopyObject().(client.Object)

	// Apply mutation.
	mutate(obj)

	// Pre-compute the reverse patch (mutated → original).
	revertPatch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	revertBytes, err := revertPatch.Data(original)
	if err != nil {
		e.Fatalf("computing revert patch for %s %q: %v", kind, key, err)
	}

	// Apply forward patch.
	if err := cl.Patch(e.Context(), obj, client.MergeFrom(original)); err != nil {
		e.Fatalf("patching %s %q: %v", kind, key, err)
	}

	// Cleanup: revert.
	e.Cleanup(func() {
		fresh := PT(new(T))

		if err := cl.Get(e.Context(), key, fresh); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: getting %s %q for revert: %v", kind, key, err)
			return
		}

		if err := cl.Patch(e.Context(), fresh,
			client.RawPatch(types.MergePatchType, revertBytes)); err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			e.Errorf("cleanup: reverting patch on %s %q: %v", kind, key, err)
		}
	})
}
