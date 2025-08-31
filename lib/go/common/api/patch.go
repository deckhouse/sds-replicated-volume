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

var ConflictRetryBackoff = wait.Backoff{
	Steps:    6,
	Duration: 1 * time.Millisecond,
	Cap:      50 * time.Millisecond,
	Factor:   2.0,
	Jitter:   0.25,
}

var ErrReloadDidNotHappen = errors.New("resource reload did not happen")

func PatchStatus[T client.Object](
	ctx context.Context,
	cl client.Client,
	resource T,
	patchFn func(resource T) error,
) error {
	assertNonNilPtrToStruct(resource)

	var conflictedResourceVersion string
	var patchErr error

	err := retry.RetryOnConflict(
		ConflictRetryBackoff,
		func() error {
			resourceVersion := resource.GetResourceVersion()

			if resourceVersion == conflictedResourceVersion {
				err := cl.Get(ctx, client.ObjectKeyFromObject(resource), resource)
				if err != nil {
					return err
				}
				if resource.GetResourceVersion() == conflictedResourceVersion {
					return ErrReloadDidNotHappen
				}
			}

			patch := client.MergeFromWithOptions(
				resource.DeepCopyObject().(client.Object),
				client.MergeFromWithOptimisticLock{},
			)

			if patchErr = patchFn(resource); patchErr != nil {
				return nil
			}

			err := cl.Status().Patch(ctx, resource, patch)

			if kerrors.IsConflict(err) {
				conflictedResourceVersion = resourceVersion
			}

			return err
		},
	)

	if err != nil {
		return err
	}
	return patchErr
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
