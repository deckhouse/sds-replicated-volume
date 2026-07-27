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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nodeWithLabels builds a Node named worker-1 with exactly these labels. The
// name is fixed on purpose: none of the snapshot logic depends on it, so a
// parameter would only invite the reader to look for a meaning it does not have.
func nodeWithLabels(labels map[string]string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Labels: labels}}
}

var _ = Describe("snapshotNodeLabel", func() {
	It("records the value of a label that is set", func() {
		node := nodeWithLabels(map[string]string{ZoneLabelKey: "zone-a", "other": "x"})
		Expect(snapshotNodeLabel(node, ZoneLabelKey)).To(Equal(nodeLabelSnapshot{
			NodeName: "worker-1", Key: ZoneLabelKey, Value: "zone-a", Existed: true,
		}))
	})

	It("distinguishes an absent label from an empty one", func() {
		absent := snapshotNodeLabel(nodeWithLabels(map[string]string{"other": "x"}), ZoneLabelKey)
		empty := snapshotNodeLabel(nodeWithLabels(map[string]string{ZoneLabelKey: ""}), ZoneLabelKey)

		Expect(absent.Existed).To(BeFalse())
		Expect(empty.Existed).To(BeTrue())
		Expect(absent).NotTo(Equal(empty), "restoring these two states must differ")
	})

	It("handles a node without any labels", func() {
		snap := snapshotNodeLabel(nodeWithLabels(nil), ZoneLabelKey)
		Expect(snap.Existed).To(BeFalse())
		Expect(snap.Value).To(BeEmpty())
	})

	It("renders both states readably", func() {
		Expect(nodeLabelSnapshot{NodeName: "w1", Key: "k", Value: "v", Existed: true}.String()).
			To(Equal("w1: k=v"))
		Expect(nodeLabelSnapshot{NodeName: "w1", Key: "k"}.String()).
			To(Equal("w1: k absent"))
	})
})

var _ = Describe("nodeLabelMergePatch", func() {
	It("sets the label without touching the other ones", func() {
		payload, err := nodeLabelMergePatch(ZoneLabelKey, "zone-a", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(payload)).To(Equal(
			`{"metadata":{"labels":{"topology.kubernetes.io/zone":"zone-a"}}}`))
	})

	It("removes the label with a JSON null, which is how a merge patch deletes a key", func() {
		payload, err := nodeLabelMergePatch(ZoneLabelKey, "ignored", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(payload)).To(Equal(
			`{"metadata":{"labels":{"topology.kubernetes.io/zone":null}}}`))
	})

	It("restores an empty label as an empty string rather than as a deletion", func() {
		payload, err := nodeLabelMergePatch("e2e.example.com/pool", "", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(payload)).To(Equal(
			`{"metadata":{"labels":{"e2e.example.com/pool":""}}}`))
	})

	It("escapes the value instead of producing broken JSON", func() {
		payload, err := nodeLabelMergePatch("k", `a"b`, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(payload)).To(Equal(`{"metadata":{"labels":{"k":"a\"b"}}}`))
	})
})
