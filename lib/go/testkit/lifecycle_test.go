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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

var _ = Describe("Lifecycle", func() {
	Describe("CreateExpect", func() {
		It("is a no-op when Client is nil", func() {
			buildCalled := false
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{
					OnBuild: func(_ context.Context) *testObj {
						buildCalled = true
						return makeTestObj("test-lc", "Pending")
					},
				})

			to.CreateExpect(context.Background(), Succeed())
			Expect(buildCalled).To(BeFalse(), "OnBuild should not be called when Client is nil")
		})
	})

	Describe("Create", func() {
		It("is a no-op when Client is nil", func() {
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{
					OnBuild: func(_ context.Context) *testObj {
						return makeTestObj("test-lc", "Pending")
					},
				})

			Expect(func() { to.Create(context.Background()) }).ToNot(Panic())
		})
	})

	Describe("GetExpect", func() {
		It("is a no-op when Client is nil", func() {
			newEmptyCalled := false
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{
					OnNewEmpty: func() *testObj {
						newEmptyCalled = true
						return &testObj{}
					},
				})

			to.GetExpect(context.Background(), Succeed())
			Expect(newEmptyCalled).To(BeFalse(), "OnNewEmpty should not be called when Client is nil")
		})
	})

	Describe("Get", func() {
		It("is a no-op when Client is nil", func() {
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{
					OnNewEmpty: func() *testObj { return &testObj{} },
				})

			Expect(func() { to.Get(context.Background()) }).ToNot(Panic())
		})
	})

	Describe("UpdateExpect", func() {
		It("is a no-op when Client is nil", func() {
			mutateCalled := false
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{})

			to.UpdateExpect(context.Background(), func(_ *testObj) {
				mutateCalled = true
			}, Succeed())
			Expect(mutateCalled).To(BeFalse(), "mutate should not be called when Client is nil")
		})
	})

	Describe("Update", func() {
		It("is a no-op when Client is nil", func() {
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{})

			Expect(func() { to.Update(context.Background(), func(_ *testObj) {}) }).ToNot(Panic())
		})
	})

	Describe("teardown", func() {
		It("calls OnTeardown when set", func() {
			called := false
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{
					OnTeardown: func() { called = true },
				})

			to.teardown()
			Expect(called).To(BeTrue())
		})

		It("calls unwatchSelf when Standalone", func() {
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{Standalone: true})

			Expect(func() { to.teardown() }).ToNot(Panic())
			Expect(to.InformerRegs).To(BeNil())
		})

		It("is a no-op when not Standalone and no OnTeardown", func() {
			to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-lc",
				Lifecycle[*testObj]{})

			Expect(func() { to.teardown() }).ToNot(Panic())
		})
	})

	Describe("Lifecycle zero value", func() {
		It("all fields are nil", func() {
			lc := Lifecycle[*testObj]{}
			Expect(lc.Debugger).To(BeNil())
			Expect(lc.OnSetup).To(BeNil())
			Expect(lc.OnTeardown).To(BeNil())
			Expect(lc.OnBuild).To(BeNil())
			Expect(lc.OnNewEmpty).To(BeNil())
		})
	})
})

var _ = Describe("awaitDeleteSync", func() {
	It("unblocks when a snapshot with a higher RV arrives", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "ds-rv",
			Lifecycle[*testObj]{})

		o := makeTestObj("ds-rv", "Healthy")
		o.SetResourceVersion("100")
		to.InjectEvent(watch.Added, o)

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitDeleteSync(context.Background(), 100)
		}()

		time.Sleep(10 * time.Millisecond)
		select {
		case <-done:
			Fail("awaitDeleteSync returned before new RV arrived")
		default:
		}

		o2 := makeTestObj("ds-rv", "Healthy")
		o2.SetResourceVersion("101")
		to.InjectEvent(watch.Modified, o2)

		Eventually(done).WithTimeout(time.Second).Should(BeClosed())
	})

	It("unblocks when the object is deleted", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "ds-del",
			Lifecycle[*testObj]{})

		o := makeTestObj("ds-del", "Healthy")
		o.SetResourceVersion("50")
		to.InjectEvent(watch.Added, o)

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitDeleteSync(context.Background(), 50)
		}()

		time.Sleep(10 * time.Millisecond)
		select {
		case <-done:
			Fail("awaitDeleteSync returned before deletion")
		default:
		}

		to.InjectEvent(watch.Deleted, o)

		Eventually(done).WithTimeout(time.Second).Should(BeClosed())
	})

	It("returns immediately if already deleted", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "ds-already",
			Lifecycle[*testObj]{})

		o := makeTestObj("ds-already", "Healthy")
		o.SetResourceVersion("10")
		to.InjectEvent(watch.Added, o)
		to.InjectEvent(watch.Deleted, o)

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitDeleteSync(context.Background(), 10)
		}()

		Eventually(done).WithTimeout(time.Second).Should(BeClosed())
	})
})
