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

package framework

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

func newTestRVAForTest(name string) *TestRVA {
	return &TestRVA{
		TrackedObject: tk.NewTrackedObject(nil, nil, schema.GroupVersionKind{}, name, tk.Lifecycle[*v1alpha1.ReplicatedVolumeAttachment]{}),
	}
}

var _ = Describe("TestRVA builder", func() {
	It("Node sets nodeName", func() {
		trva := newTestRVAForTest("test-rva").Node("worker-1")
		rva := trva.buildObject()
		Expect(rva.Spec.NodeName).To(Equal("worker-1"))
	})

	It("unset Node leaves empty", func() {
		trva := newTestRVAForTest("test-rva")
		rva := trva.buildObject()
		Expect(rva.Spec.NodeName).To(BeEmpty())
	})

	It("RVName sets replicatedVolumeName", func() {
		trva := newTestRVAForTest("test-rva").RVName("my-rv")
		rva := trva.buildObject()
		Expect(rva.Spec.ReplicatedVolumeName).To(Equal("my-rv"))
	})

	It("name is set correctly", func() {
		trva := newTestRVAForTest("my-rva")
		Expect(trva.Name()).To(Equal("my-rva"))
		rva := trva.buildObject()
		Expect(rva.Name).To(Equal("my-rva"))
	})

	It("fluent chain returns same instance", func() {
		trva := newTestRVAForTest("test-rva")
		same := trva.Node("n1").RVName("rv")
		Expect(same).To(BeIdenticalTo(trva))
	})

	It("Create is no-op with nil client", func() {
		trva := newTestRVAForTest("test-rva").RVName("rv").Node("n1")
		trva.Create(context.Background())
		Expect(trva.Name()).To(Equal("test-rva"))
	})

	It("GetExpect is no-op with nil client", func() {
		trva := newTestRVAForTest("test-rva")
		trva.GetExpect(context.Background(), Succeed())
		Expect(trva.Name()).To(Equal("test-rva"))
	})

	It("Get is no-op with nil client", func() {
		trva := newTestRVAForTest("test-rva")
		trva.Get(context.Background())
		Expect(trva.Name()).To(Equal("test-rva"))
	})
})
