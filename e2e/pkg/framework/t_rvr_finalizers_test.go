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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// recordedPatch is one write the patch cores asked the cluster for, rendered
// the way the API server would receive it.
type recordedPatch struct {
	Name string
	Type types.PatchType
	Data string
}

// stubPatcher is the cluster seam of the patch cores in unit tests: it records
// every patch and answers with a scripted error.
type stubPatcher struct {
	err     error
	patches []recordedPatch
}

func (s *stubPatcher) Patch(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
	data, err := patch.Data(obj)
	Expect(err).NotTo(HaveOccurred())
	s.patches = append(s.patches, recordedPatch{Name: obj.GetName(), Type: patch.Type(), Data: string(data)})
	return s.err
}

var _ = Describe("removeRVRFinalizers", func() {
	ctx := context.Background()

	It("clears the finalizers of exactly one replica, in a single write", func() {
		patcher := &stubPatcher{}

		Expect(removeRVRFinalizers(ctx, patcher, "e2e-rv-0")).To(Succeed())

		Expect(patcher.patches).To(Equal([]recordedPatch{{
			Name: "e2e-rv-0",
			Type: types.MergePatchType,
			Data: `{"metadata":{"finalizers":null}}`,
		}}))
	})

	It("treats a replica that vanished as the outcome it wanted", func() {
		patcher := &stubPatcher{err: apierrors.NewNotFound(
			schema.GroupResource{Group: "storage.deckhouse.io", Resource: "replicatedvolumereplicas"}, "e2e-rv-0")}

		Expect(removeRVRFinalizers(ctx, patcher, "e2e-rv-0")).To(Succeed())
	})

	It("names the replica it could not patch", func() {
		patcher := &stubPatcher{err: errors.New("apiserver said no")}

		err := removeRVRFinalizers(ctx, patcher, "e2e-rv-0")

		Expect(err).To(MatchError(ContainSubstring("removing the finalizers of e2e-rv-0")))
		Expect(err).To(MatchError(ContainSubstring("apiserver said no")))
	})
})
