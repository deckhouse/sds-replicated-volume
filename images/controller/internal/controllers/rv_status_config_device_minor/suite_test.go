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

package rvstatusconfigdeviceminor_test

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

func TestRvStatusConfigDeviceMinor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvStatusConfigDeviceMinor Suite")
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func InterceptGet[T client.Object](
	intercept func(T) error,
) interceptor.Funcs {
	var zero T
	tType := reflect.TypeOf(zero)
	if tType == nil {
		panic("cannot determine type")
	}

	return interceptor.Funcs{
		Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if reflect.TypeOf(obj).AssignableTo(tType) {
				return intercept(obj.(T))
			}
			return client.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if reflect.TypeOf(list).Elem().Elem().AssignableTo(tType) {
				items := reflect.ValueOf(list).Elem().FieldByName("Items")
				if items.IsValid() && items.Kind() == reflect.Slice {
					for i := 0; i < items.Len(); i++ {
						item := items.Index(i).Addr().Interface().(T)
						if err := intercept(item); err != nil {
							return err
						}
					}
				}
			}
			return client.List(ctx, list, opts...)
		},
	}
}
