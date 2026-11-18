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

package sdnavailability

import (
	"errors"
	"testing"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestAvailability(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "SDN startup") }

var _ = Describe("startup Kind discovery", func() {
	It("does not discover while off and requires both Kinds when on", func() {
		enabled, err := Enabled(false, nil, logr.Discard())
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeFalse())
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{sdn.SchemeGroupVersion})
		enabled, err = Enabled(true, mapper, logr.Discard())
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeFalse())
		mapper.Add(sdn.NodeNetworkStatusGVK, meta.RESTScopeRoot)
		enabled, err = Enabled(true, mapper, logr.Discard())
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeFalse())
		mapper.Add(sdn.SystemNetworkGVK, meta.RESTScopeRoot)
		enabled, err = Enabled(true, mapper, logr.Discard())
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeTrue())
	})
	It("propagates genuine discovery errors instead of disabling SDN", func() {
		failure := errors.New("discovery forbidden")
		enabled, err := Enabled(true, failingMapper{failure: failure}, logr.Discard())
		Expect(enabled).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("discovery forbidden")))
		Expect(errors.Is(err, failure)).To(BeTrue())
	})
})

type failingMapper struct {
	meta.RESTMapper
	failure error
}

func (m failingMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, m.failure
}
