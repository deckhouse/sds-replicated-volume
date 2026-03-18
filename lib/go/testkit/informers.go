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

package testkit

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InformerReg stores a handler registration and its informer for cleanup.
type InformerReg struct {
	informer cache.Informer
	reg      toolscache.ResourceEventHandlerRegistration
}

// RemoveInformerRegs removes all registered handlers from their informers.
func RemoveInformerRegs(regs []InformerReg) {
	for _, r := range regs {
		_ = r.informer.RemoveEventHandler(r.reg)
	}
}

// RegisterInformer registers an informer event handler for type T
// identified by GVK. filter selects which objects to process; handler
// receives the event type and the typed object.
func RegisterInformer[T client.Object](
	ctx context.Context,
	c cache.Cache,
	gvk schema.GroupVersionKind,
	desc string,
	filter func(T) bool,
	handler func(watch.EventType, T),
) InformerReg {
	inf, err := c.GetInformerForKind(ctx, gvk)
	if err != nil {
		Fail(fmt.Sprintf("%s: failed to get informer: %v", desc, err))
	}

	h := toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			typed, ok := obj.(T)
			if !ok || !filter(typed) {
				return
			}
			handler(watch.Added, typed)
		},
		UpdateFunc: func(_, newObj interface{}) {
			typed, ok := newObj.(T)
			if !ok || !filter(typed) {
				return
			}
			handler(watch.Modified, typed)
		},
		DeleteFunc: func(obj interface{}) {
			if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = d.Obj
			}
			typed, ok := obj.(T)
			if !ok || !filter(typed) {
				return
			}
			handler(watch.Deleted, typed)
		},
	}

	reg, err := inf.AddEventHandler(h)
	if err != nil {
		Fail(fmt.Sprintf("%s: failed to add handler: %v", desc, err))
	}
	return InformerReg{informer: inf, reg: reg}
}
