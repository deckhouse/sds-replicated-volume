package api

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConflictRetryBackoff is the backoff policy used by PatchWithConflictRetry and
// PatchStatusWithConflictRetry to retry conditional patches on transient conflicts.
var ConflictRetryBackoff = wait.Backoff{
	Steps:    6,
	Duration: 1 * time.Millisecond,
	Cap:      50 * time.Millisecond,
	Factor:   2.0,
	Jitter:   0.25,
}

var errReloadDidNotHappen = errors.New("resource reload did not happen")

// PatchStatusWithConflictRetry applies a conditional, retriable merge-patch to the Status subresource.
//
// The patch is conditional via optimistic locking: it uses MergeFrom with
// a resourceVersion precondition, so the update only succeeds if the current
// resourceVersion matches. On 409 Conflict, the operation is retried using the
// ConflictRetryBackoff policy. If a conflict is detected and reloading the
// resource yields the same resourceVersion, the condition is treated as
// transient and retried as well; no special error is returned for this case.
//
// The provided patchFn must mutate the given object. If patchFn returns an
// error, no patch is sent and that error is returned.
//
// The resource must be a non-nil pointer to a struct; otherwise this function panics.
func PatchStatusWithConflictRetry[T client.Object](
	ctx context.Context,
	cl client.Client,
	resource T,
	patchFn func(resource T) error,
) error {
	return patch(ctx, cl, true, resource, patchFn)
}

// PatchWithConflictRetry applies a conditional, retriable merge-patch to the main resource (spec/metadata).
//
// The patch is conditional via optimistic locking: it uses MergeFrom with
// a resourceVersion precondition, so the update only succeeds if the current
// resourceVersion matches. On 409 Conflict, the operation is retried using the
// ConflictRetryBackoff policy. If a conflict is detected and reloading the
// resource yields the same resourceVersion, the condition is treated as
// transient and retried as well; no special error is returned for this case.
//
// The provided patchFn must mutate the given object. If patchFn returns an
// error, no patch is sent and that error is returned.
//
// The resource must be a non-nil pointer to a struct; otherwise this function panics.
func PatchWithConflictRetry[T client.Object](
	ctx context.Context,
	cl client.Client,
	resource T,
	patchFn func(resource T) error,
) error {
	return patch(ctx, cl, false, resource, patchFn)
}

func patch[T client.Object](
	ctx context.Context,
	cl client.Client,
	status bool,
	resource T,
	patchFn func(resource T) error,
) error {
	assertNonNilPtrToStruct(resource)

	var conflictedResourceVersion string

	return retry.OnError(
		ConflictRetryBackoff,
		func(err error) bool {
			return kerrors.IsConflict(err) || err == errReloadDidNotHappen
		},
		func() error {
			resourceVersion := resource.GetResourceVersion()

			if resourceVersion == conflictedResourceVersion {
				err := cl.Get(ctx, client.ObjectKeyFromObject(resource), resource)
				if err != nil {
					return err
				}
				if resource.GetResourceVersion() == conflictedResourceVersion {
					return errReloadDidNotHappen
				}
			}

			patch := client.MergeFromWithOptions(
				resource.DeepCopyObject().(client.Object),
				client.MergeFromWithOptimisticLock{},
			)

			if err := patchFn(resource); err != nil {
				return err
			}

			var err error
			if status {
				err = cl.Status().Patch(ctx, resource, patch)
			} else {
				err = cl.Patch(ctx, resource, patch)
			}
			if kerrors.IsConflict(err) {
				conflictedResourceVersion = resourceVersion
			}

			return err
		},
	)
}

func assertNonNilPtrToStruct[T any](obj T) {
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Pointer || rt.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("T must be a pointer to a struct; got %s", rt))
	}
	if reflect.ValueOf(obj).IsNil() {
		panic("obj must not be nil")
	}
}
