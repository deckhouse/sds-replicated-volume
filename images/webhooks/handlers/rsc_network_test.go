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

package handlers

import (
	"context"
	"errors"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("RSC SystemNetwork validation", func() {
	It("does not initialize an SDN client for Internal or omitted networks", func() {
		for _, names := range [][]string{nil, {"Internal"}} {
			rsc := &srv.ReplicatedStorageClass{Spec: srv.ReplicatedStorageClassSpec{SystemNetworkNames: names}}
			result, err := validateSystemNetworks(context.Background(), rsc, false, func() (client.Reader, error) {
				Fail("Internal networking must not access SDN")
				return nil, nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeTrue())
		}
	})

	It("requires SDN to be enabled before accessing a named network", func() {
		rsc := &srv.ReplicatedStorageClass{Spec: srv.ReplicatedStorageClassSpec{SystemNetworkNames: []string{"replication"}}}
		result, err := validateSystemNetworks(context.Background(), rsc, false, func() (client.Reader, error) {
			Fail("disabled SDN must not be queried")
			return nil, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Valid).To(BeFalse())
		Expect(result.Message).To(ContainSubstring("requires the sdn module to be enabled"))
	})

	It("checks the currently selected network and requires IPAM", func() {
		scheme := runtime.NewScheme()
		Expect(sdn.AddToScheme(scheme)).To(Succeed())
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&sdn.SystemNetwork{ObjectMeta: metav1.ObjectMeta{Name: "ready"}, Spec: sdn.SystemNetworkSpec{IPAM: &sdn.SystemNetworkIPAMSpec{}}},
			&sdn.SystemNetwork{ObjectMeta: metav1.ObjectMeta{Name: "unconfigured"}},
		).Build()
		rsc := &srv.ReplicatedStorageClass{}
		for _, name := range []string{"ready", "unconfigured", "ready", "Internal"} {
			rsc.Spec.SystemNetworkNames = []string{name}
			result, err := validateSystemNetworks(context.Background(), rsc, true, func() (client.Reader, error) { return cl, nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(Equal(name != "unconfigured"))
			if !result.Valid {
				Expect(result.Message).To(ContainSubstring("spec.ipam"))
			}
		}
	})

	DescribeTable("fails closed when SDN cannot be read", func(readErr error, rejection string) {
		scheme := runtime.NewScheme()
		Expect(sdn.AddToScheme(scheme)).To(Succeed())
		cl := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return readErr
			},
		}).Build()
		rsc := &srv.ReplicatedStorageClass{Spec: srv.ReplicatedStorageClassSpec{SystemNetworkNames: []string{"replication"}}}
		result, err := validateSystemNetworks(context.Background(), rsc, true, func() (client.Reader, error) { return cl, nil })
		if rejection != "" {
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Valid).To(BeFalse())
			Expect(result.Message).To(ContainSubstring("spec.systemNetworkNames:"))
			Expect(result.Message).To(ContainSubstring("\"replication\""))
			Expect(result.Message).To(ContainSubstring(rejection))
			return
		}
		Expect(result).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("spec.systemNetworkNames: cannot get SystemNetwork \"replication\"")))
		Expect(errors.Is(err, readErr)).To(BeTrue())
	},
		Entry("API unavailable", &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "network.deckhouse.io", Kind: "SystemNetwork"}}, "SDN API unavailable"),
		Entry("network absent", apierrors.NewNotFound(schema.GroupResource{Group: "network.deckhouse.io", Resource: "systemnetworks"}, "replication"), "not found"),
		Entry("access forbidden", apierrors.NewForbidden(schema.GroupResource{Group: "network.deckhouse.io", Resource: "systemnetworks"}, "replication", errors.New("denied")), ""),
		Entry("transport failure", errors.New("connection refused"), ""),
	)

	It("fails closed if the direct client cannot be initialized", func() {
		rsc := &srv.ReplicatedStorageClass{Spec: srv.ReplicatedStorageClassSpec{SystemNetworkNames: []string{"replication"}}}
		result, err := validateSystemNetworks(context.Background(), rsc, true, func() (client.Reader, error) { return nil, errors.New("unavailable") })
		Expect(result).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("cannot access SystemNetwork \"replication\"")))
	})
})
