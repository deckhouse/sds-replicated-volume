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

package drbdr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
)

type portRegistryListenFunc func(context.Context, string, string) (net.Listener, error)

func (f portRegistryListenFunc) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	return f(ctx, network, address)
}

type portRegistryTestListener struct {
	net.Listener
	closed bool
}

func (l *portRegistryTestListener) Close() error {
	l.closed = true
	return nil
}

func TestPortRegistryListenErrors(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PortRegistry listen errors")
}

var _ = Describe("PortRegistry listen errors", func() {
	var reg *PortRegistry
	var addresses []string
	BeforeEach(func() {
		sch, err := scheme.New()
		Expect(err).NotTo(HaveOccurred())
		cl := fake.NewClientBuilder().WithScheme(sch).
			WithIndex(&v1alpha1.DRBDResource{}, indexes.IndexFieldDRBDRByNodeName, func(client.Object) []string { return nil }).Build()
		pc := NewDRBDPortCache()
		pc.BeginDump()
		pc.EndDump()
		reg = NewPortRegistry(cl, "test-node", pc, 19000, 19002, time.Minute)
		addresses = nil
	})

	It("skips wrapped EADDRINUSE and reserves the next successful port", func() {
		listener := &portRegistryTestListener{}
		reg.listenCfg = portRegistryListenFunc(func(_ context.Context, network, address string) (net.Listener, error) {
			Expect(network).To(Equal("tcp"))
			addresses = append(addresses, address)
			if len(addresses) == 1 {
				return nil, fmt.Errorf("bind: %w", syscall.EADDRINUSE)
			}
			return listener, nil
		})
		port, err := reg.Allocate(context.Background(), "192.0.2.1")
		Expect(err).NotTo(HaveOccurred())
		Expect(port).To(Equal(uint(19001)))
		Expect(addresses).To(Equal([]string{"192.0.2.1:19000", "192.0.2.1:19001"}))
		Expect(listener.closed).To(BeTrue())
		Expect(reg.pending["192.0.2.1"]).To(HaveLen(1))
		Expect(reg.pending["192.0.2.1"][0].port).To(Equal(port))
	})

	DescribeTable("returns non-busy errors without probing remaining ports", func(cause error) {
		original := fmt.Errorf("listen failure: %w", cause)
		reg.listenCfg = portRegistryListenFunc(func(_ context.Context, _, address string) (net.Listener, error) {
			addresses = append(addresses, address)
			return nil, original
		})
		port, err := reg.Allocate(context.Background(), "192.0.2.1")
		Expect(port).To(BeZero())
		Expect(err).To(MatchError(ContainSubstring("192.0.2.1:19000")))
		Expect(err).To(MatchError(ContainSubstring("listen")))
		Expect(err).To(MatchError(ContainSubstring(cause.Error())))
		Expect(err).To(MatchError(ContainSubstring(original.Error())))
		Expect(errors.Is(err, original)).To(BeTrue())
		Expect(errors.Is(err, cause)).To(BeTrue())
		Expect(addresses).To(Equal([]string{"192.0.2.1:19000"}))
		Expect(reg.pending).To(BeEmpty())
	},
		Entry("unavailable address", syscall.EADDRNOTAVAIL),
		Entry("permission denied", syscall.EACCES),
		Entry("canceled operation", context.Canceled),
	)
})
