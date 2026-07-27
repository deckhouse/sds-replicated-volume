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
	"errors"
	"maps"

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

// labelPatch is one write a label core asked the cluster for.
type labelPatch struct {
	Node  string
	Key   string
	Value string
	Set   bool
}

// stubNodeLabelAPI is the cluster seam of the label cores in unit tests. Reads
// are answered from a label table that the recorded patches also apply to, so a
// restore can be judged by what the cluster would hold afterwards and not only
// by the patches that were sent.
type stubNodeLabelAPI struct {
	labels   map[string]map[string]string
	readErr  map[string]error
	patchErr map[string]error

	reads   []string
	patches []labelPatch
}

func newStubNodeLabelAPI(labels map[string]map[string]string) *stubNodeLabelAPI {
	return &stubNodeLabelAPI{
		labels:   labels,
		readErr:  map[string]error{},
		patchErr: map[string]error{},
	}
}

func (s *stubNodeLabelAPI) getNodeLive(_ context.Context, nodeName string) (*corev1.Node, error) {
	s.reads = append(s.reads, nodeName)
	if err := s.readErr[nodeName]; err != nil {
		return nil, err
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Labels: maps.Clone(s.labels[nodeName])},
	}, nil
}

func (s *stubNodeLabelAPI) patchNodeLabel(_ context.Context, nodeName, key, value string, set bool) error {
	s.patches = append(s.patches, labelPatch{Node: nodeName, Key: key, Value: value, Set: set})
	if err := s.patchErr[nodeName]; err != nil {
		return err
	}
	if s.labels[nodeName] == nil {
		s.labels[nodeName] = map[string]string{}
	}
	if set {
		s.labels[nodeName][key] = value
	} else {
		delete(s.labels[nodeName], key)
	}
	return nil
}

// patchedNodes returns the nodes of all recorded patches, in order.
func (s *stubNodeLabelAPI) patchedNodes() []string {
	out := make([]string, len(s.patches))
	for i := range s.patches {
		out[i] = s.patches[i].Node
	}
	return out
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

var _ = Describe("readNodeLabel", func() {
	It("reads the label off a live node", func() {
		stub := newStubNodeLabelAPI(map[string]map[string]string{"worker-a": {ZoneLabelKey: "zone-a"}})

		snap, err := readNodeLabel(context.Background(), stub, "worker-a", ZoneLabelKey)

		Expect(err).NotTo(HaveOccurred())
		Expect(snap).To(Equal(nodeLabelSnapshot{
			NodeName: "worker-a", Key: ZoneLabelKey, Value: "zone-a", Existed: true,
		}))
	})

	It("names the node it could not read", func() {
		stub := newStubNodeLabelAPI(map[string]map[string]string{"worker-a": nil})
		stub.readErr["worker-a"] = errors.New("connection refused")

		_, err := readNodeLabel(context.Background(), stub, "worker-a", ZoneLabelKey)

		Expect(err).To(MatchError(ContainSubstring(`reading node "worker-a"`)))
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})
})

var _ = Describe("SetNodeLabel lifecycle", func() {
	ctx := context.Background()

	// The three nodes cover every prior state a restore has to reproduce: a
	// label with a value, an empty label, and no label at all.
	newStub := func() *stubNodeLabelAPI {
		return newStubNodeLabelAPI(map[string]map[string]string{
			"worker-a": {ZoneLabelKey: "zone-a"},
			"worker-b": {ZoneLabelKey: ""},
			"worker-c": {"other": "x"},
		})
	}
	values := map[string]string{"worker-c": "zone-z", "worker-a": "zone-x", "worker-b": "zone-y"}

	It("snapshots every node before it writes anything", func() {
		stub := newStub()

		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)

		Expect(err).NotTo(HaveOccurred())
		Expect(stub.patches).To(BeEmpty(),
			"the whole restore must be knowable before the first mutation")
		Expect(stub.reads).To(Equal([]string{"worker-a", "worker-b", "worker-c"}))
		Expect(plan.snapshots).To(Equal([]nodeLabelSnapshot{
			{NodeName: "worker-a", Key: ZoneLabelKey, Value: "zone-a", Existed: true},
			{NodeName: "worker-b", Key: ZoneLabelKey, Value: "", Existed: true},
			{NodeName: "worker-c", Key: ZoneLabelKey, Value: "", Existed: false},
		}))
	})

	It("labels every node with its own value", func() {
		stub := newStub()
		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)
		Expect(err).NotTo(HaveOccurred())

		Expect(plan.apply(ctx, stub)).To(Succeed())

		Expect(stub.patches).To(Equal([]labelPatch{
			{Node: "worker-a", Key: ZoneLabelKey, Value: "zone-x", Set: true},
			{Node: "worker-b", Key: ZoneLabelKey, Value: "zone-y", Set: true},
			{Node: "worker-c", Key: ZoneLabelKey, Value: "zone-z", Set: true},
		}))
	})

	It("restores an absent, an empty and a set label exactly", func() {
		stub := newStub()
		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.apply(ctx, stub)).To(Succeed())

		Expect(plan.restore(ctx, stub)).To(Succeed())

		Expect(stub.patches[3:]).To(Equal([]labelPatch{
			{Node: "worker-a", Key: ZoneLabelKey, Value: "zone-a", Set: true},
			{Node: "worker-b", Key: ZoneLabelKey, Value: "", Set: true},
			{Node: "worker-c", Key: ZoneLabelKey, Value: "", Set: false},
		}))
		Expect(stub.labels["worker-a"]).To(Equal(map[string]string{ZoneLabelKey: "zone-a"}))
		Expect(stub.labels["worker-b"]).To(Equal(map[string]string{ZoneLabelKey: ""}),
			"an empty label must come back empty, not deleted")
		Expect(stub.labels["worker-c"]).To(Equal(map[string]string{"other": "x"}),
			"a label that did not exist must be deleted, not emptied")
	})

	It("restores what the snapshot saw, not what the node holds later", func() {
		stub := newStub()
		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.apply(ctx, stub)).To(Succeed())
		stub.labels["worker-a"][ZoneLabelKey] = "written-by-someone-else"

		Expect(plan.restore(ctx, stub)).To(Succeed())

		Expect(stub.labels["worker-a"]).To(Equal(map[string]string{ZoneLabelKey: "zone-a"}))
	})

	It("restores the nodes a failed labelling never reached", func() {
		stub := newStub()
		stub.patchErr["worker-b"] = errors.New("apiserver said no")
		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)
		Expect(err).NotTo(HaveOccurred())

		err = plan.apply(ctx, stub)

		Expect(err).To(MatchError(ContainSubstring(
			`labelling node "worker-b" with topology.kubernetes.io/zone=zone-y`)))
		Expect(err).To(MatchError(ContainSubstring("apiserver said no")))
		Expect(stub.labels["worker-c"]).To(Equal(map[string]string{"other": "x"}),
			"labelling must stop at the first failure")

		// The cluster recovers, and the cleanup registered before the first
		// write puts every node back — the one it labelled, the one it failed
		// on, and the one it never reached.
		delete(stub.patchErr, "worker-b")
		Expect(plan.restore(ctx, stub)).To(Succeed())
		Expect(stub.labels["worker-a"]).To(Equal(map[string]string{ZoneLabelKey: "zone-a"}))
		Expect(stub.labels["worker-b"]).To(Equal(map[string]string{ZoneLabelKey: ""}))
		Expect(stub.labels["worker-c"]).To(Equal(map[string]string{"other": "x"}))
	})

	It("names the node it could not restore", func() {
		stub := newStub()
		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.apply(ctx, stub)).To(Succeed())
		stub.patchErr["worker-b"] = errors.New("apiserver said no")

		err = plan.restore(ctx, stub)

		Expect(err).To(MatchError(ContainSubstring("restoring worker-b: topology.kubernetes.io/zone=")))
		Expect(err).To(MatchError(ContainSubstring("apiserver said no")))
	})

	It("rejects an empty key before touching the cluster", func() {
		stub := newStub()

		_, err := planNodeLabel(ctx, stub, "", values)

		Expect(err).To(MatchError("label key must not be empty"))
		Expect(stub.reads).To(BeEmpty())
		Expect(stub.patches).To(BeEmpty())
	})

	It("writes nothing when a node cannot be read", func() {
		stub := newStub()
		stub.readErr["worker-b"] = errors.New("connection refused")

		_, err := planNodeLabel(ctx, stub, ZoneLabelKey, values)

		Expect(err).To(MatchError(ContainSubstring(`reading node "worker-b"`)))
		Expect(stub.patches).To(BeEmpty())
	})

	It("does not let the caller's map change what gets written", func() {
		stub := newStub()
		caller := maps.Clone(values)
		plan, err := planNodeLabel(ctx, stub, ZoneLabelKey, caller)
		Expect(err).NotTo(HaveOccurred())

		caller["worker-a"] = "zone-hijacked"
		Expect(plan.apply(ctx, stub)).To(Succeed())

		Expect(stub.labels["worker-a"]).To(Equal(map[string]string{ZoneLabelKey: "zone-x"}))
	})
})
