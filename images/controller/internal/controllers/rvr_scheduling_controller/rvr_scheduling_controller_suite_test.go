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

package rvrschedulingcontroller_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRvrSchedulingController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrSchedulingController Suite")
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

// InterceptGet creates an interceptor that modifies objects in both Get and List operations.
// If Get or List returns an error, intercept is called with a nil (zero) value of type T allowing
// the test to override the error.
func InterceptGet[T client.Object](
	intercept func(T) error,
) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if target, ok := obj.(T); ok {
				if err := cl.Get(ctx, key, obj, opts...); err != nil {
					var zero T
					if err := intercept(zero); err != nil {
						return err
					}
					return err
				}
				if err := intercept(target); err != nil {
					return err
				}
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if err := cl.List(ctx, list, opts...); err != nil {
				var zero T
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			return nil
		},
	}
}
