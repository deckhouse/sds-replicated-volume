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

package migrator

import (
	"context"

	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
)

// errTrackingClient wraps a controller-runtime client and injects errors per Get/Create call.
// A nil entry in getErrs/createErrs means "delegate to the wrapped client". Used by tests
// to simulate transient/permanent API errors and concurrency races.
type errTrackingClient struct {
	kubecl.Client
	getErrs     []error
	createErrs  []error
	getCalls    int
	createCalls int
}

func (c *errTrackingClient) Get(ctx context.Context, key kubecl.ObjectKey, obj kubecl.Object, opts ...kubecl.GetOption) error {
	idx := c.getCalls
	c.getCalls++
	if idx < len(c.getErrs) && c.getErrs[idx] != nil {
		return c.getErrs[idx]
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *errTrackingClient) Create(ctx context.Context, obj kubecl.Object, opts ...kubecl.CreateOption) error {
	idx := c.createCalls
	c.createCalls++
	if idx < len(c.createErrs) && c.createErrs[idx] != nil {
		return c.createErrs[idx]
	}
	return c.Client.Create(ctx, obj, opts...)
}
