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
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

func newTestRVRForTest(rvName string) *TestRVR {
	fullName := v1alpha1.FormatReplicatedVolumeReplicaName(rvName, 0)
	return &TestRVR{
		TrackedObject: tk.NewTrackedObject(nil, nil, schema.GroupVersionKind{}, fullName, tk.Lifecycle[*v1alpha1.ReplicatedVolumeReplica]{}),
		buildRVName:   rvName,
	}
}

var _ = Describe("TestRVR builder", func() {
	It("Node sets nodeName", func() {
		trvr := newTestRVRForTest("test-rvr").Node("worker-1")
		rvr := trvr.buildObject()
		Expect(rvr.Spec.NodeName).To(Equal("worker-1"))
	})

	It("unset Node leaves empty", func() {
		trvr := newTestRVRForTest("test-rvr")
		rvr := trvr.buildObject()
		Expect(rvr.Spec.NodeName).To(BeEmpty())
	})

	It("Type sets replica type", func() {
		trvr := newTestRVRForTest("test-rvr").Type(v1alpha1.ReplicaTypeAccess)
		rvr := trvr.buildObject()
		Expect(rvr.Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
	})

	It("unset Type leaves zero value", func() {
		trvr := newTestRVRForTest("test-rvr")
		rvr := trvr.buildObject()
		Expect(string(rvr.Spec.Type)).To(BeEmpty())
	})

	It("LVG and ThinPool set correctly", func() {
		trvr := newTestRVRForTest("test-rvr").LVG("vg1").ThinPool("tp1")
		rvr := trvr.buildObject()
		Expect(rvr.Spec.LVMVolumeGroupName).To(Equal("vg1"))
		Expect(rvr.Spec.LVMVolumeGroupThinPoolName).To(Equal("tp1"))
	})

	It("rvName is set from construction", func() {
		trvr := newTestRVRForTest("my-rv")
		rvr := trvr.buildObject()
		Expect(rvr.Spec.ReplicatedVolumeName).To(Equal("my-rv"))
	})

	It("name is formatted as rvName-id", func() {
		trvr := newTestRVRForTest("my-rvr")
		Expect(trvr.Name()).To(Equal("my-rvr-0"))
		rvr := trvr.buildObject()
		Expect(rvr.Name).To(Equal("my-rvr-0"))
	})

	It("fluent chain returns same instance", func() {
		trvr := newTestRVRForTest("test-rvr")
		same := trvr.Node("n1").Type(v1alpha1.ReplicaTypeDiskful).LVG("vg").ThinPool("tp")
		Expect(same).To(BeIdenticalTo(trvr))
	})

	It("Create is no-op with nil client", func() {
		trvr := newTestRVRForTest("rv").Node("n1")
		trvr.Create(context.Background())
		Expect(trvr.Name()).To(Equal("rv-0"))
	})

	It("GetExpect is no-op with nil client", func() {
		trvr := newTestRVRForTest("test-rvr")
		trvr.GetExpect(context.Background(), Succeed())
		Expect(trvr.Name()).To(Equal("test-rvr-0"))
	})

	It("Get is no-op with nil client", func() {
		trvr := newTestRVRForTest("test-rvr")
		trvr.Get(context.Background())
		Expect(trvr.Name()).To(Equal("test-rvr-0"))
	})
})

var _ = Describe("TestRVR DRBDR", func() {
	It("DRBDR returns wrapper", func() {
		trvr := newTestRVRForTest("test-rvr")
		tdrbdr := trvr.DRBDR()
		Expect(tdrbdr).NotTo(BeNil())
		Expect(tdrbdr.Name()).To(Equal("test-rvr-0"))
	})

	It("injectDRBDREvent routes to drbdr", func() {
		trvr := newTestRVRForTest("test-rvr")
		drbdr := &v1alpha1.DRBDResource{}
		drbdr.Name = "test-rvr-0"
		trvr.injectDRBDREvent(watch.Added, drbdr)

		Expect(trvr.DRBDR().Object()).NotTo(BeNil())
	})
})
