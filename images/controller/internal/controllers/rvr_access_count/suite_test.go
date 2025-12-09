/*
Copyright 2025 Flant JSC

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

package rvraccesscount_test

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRvrAccessCount(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrAccessCount Suite")
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// InterceptGet creates an interceptor that modifies objects in both Get and List operations.
// If Get or List returns an error, intercept is called with a nil (zero) value of type T allowing alternating the error.
func InterceptGet[T client.Object](
	intercept func(T) error,
) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			targetObj, ok := obj.(T)
			if !ok {
				return cl.Get(ctx, key, obj, opts...)
			}
			if err := cl.Get(ctx, key, obj, opts...); err != nil {
				var zero T
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			if err := intercept(targetObj); err != nil {
				return err
			}
			return nil
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			v := reflect.ValueOf(list).Elem()
			itemsField := v.FieldByName("Items")
			if !itemsField.IsValid() || itemsField.Kind() != reflect.Slice {
				return cl.List(ctx, list, opts...)
			}
			if err := cl.List(ctx, list, opts...); err != nil {
				var zero T
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			for i := 0; i < itemsField.Len(); i++ {
				item := itemsField.Index(i).Addr().Interface().(client.Object)
				if targetObj, ok := item.(T); ok {
					if err := intercept(targetObj); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
}
