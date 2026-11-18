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

package main

import (
	"testing"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSDNStartup(t *testing.T) {
	g := NewWithT(t)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{sdn.SchemeGroupVersion})
	available, err := checkSDNKind(true, mapper)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(available).To(BeFalse())
	options := cache.Options{ByObject: map[client.Object]cache.ByObject{}}
	configureSDNCache(&options, "node-a", available)
	g.Expect(options.ByObject).To(BeEmpty())
	mapper.Add(sdn.NodeNetworkStatusGVK, meta.RESTScopeRoot)
	available, err = checkSDNKind(true, mapper)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(available).To(BeTrue())
	configureSDNCache(&options, "node-a", available)
	g.Expect(options.ByObject).To(HaveLen(1))
	for _, value := range options.ByObject {
		g.Expect(value.Field.String()).To(Equal("metadata.name=node-a"))
	}
	available, err = checkSDNKind(false, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(available).To(BeFalse())
}
