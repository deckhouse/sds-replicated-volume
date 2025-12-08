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

package rvrdiskfulcount_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRVStatusConfigQuorumController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RV Status Config Quorum Controller Suite")
}

// FailOnAnyChange returns interceptor.Funcs that fail on any write operation (Create, Update, Patch, Delete, etc.)
func FailOnAnyChange(isActive func() bool) interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if isActive() {
				Fail("Create should not be called")
			}
			return cl.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if isActive() {
				Fail("Update should not be called")
			}
			return cl.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if isActive() {
				Fail("Patch should not be called")
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
		SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if isActive() {
				Fail("SubResourcePatch should not be called")
			}
			return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
		Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if isActive() {
				Fail("Apply should not be called")
			}
			return cl.Apply(ctx, obj, opts...)
		},
		SubResourceCreate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
			if isActive() {
				Fail("SubResourceCreate should not be called")
			}
			return cl.SubResource(subResourceName).Create(ctx, obj, subResource, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if isActive() {
				Fail("SubResourceUpdate should not be called")
			}
			return cl.SubResource(subResourceName).Update(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if isActive() {
				Fail("Delete should not be called")
			}
			return cl.Delete(ctx, obj, opts...)
		},
		DeleteAllOf: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
			if isActive() {
				Fail("DeleteAllOf should not be called")
			}
			return cl.DeleteAllOf(ctx, obj, opts...)
		},
	}
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}
