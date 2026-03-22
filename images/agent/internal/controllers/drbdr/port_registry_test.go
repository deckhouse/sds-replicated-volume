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

package drbdr_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
)

const portRegistryTestNode = "pr-test-node"

func readyPortCache(t *testing.T, entries ...struct {
	res  string
	ip   string
	port uint
}) *drbdr.DRBDPortCache {
	t.Helper()
	pc := drbdr.NewDRBDPortCache()
	pc.BeginDump()
	for _, e := range entries {
		pc.Add(e.res, e.ip, e.port)
	}
	pc.EndDump()
	return pc
}

func buildPortRegistryClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(sch).
		WithIndex(&v1alpha1.DRBDResource{}, indexes.IndexFieldDRBDRByNodeName, func(obj client.Object) []string {
			dr, ok := obj.(*v1alpha1.DRBDResource)
			if !ok || dr.Spec.NodeName == "" {
				return nil
			}
			return []string{dr.Spec.NodeName}
		}).
		WithObjects(objs...).
		Build()
}

func drbdResourceWithPort(name, nodeName, ip string, port uint) *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.DRBDResourceSpec{NodeName: nodeName},
		Status: v1alpha1.DRBDResourceStatus{
			Addresses: []v1alpha1.DRBDResourceAddressStatus{
				{
					SystemNetworkName: "Internal",
					Address:           v1alpha1.DRBDAddress{IPv4: ip, Port: port},
				},
			},
		},
	}
}

// allocFreePort finds a port that is free at the OS level on the loopback
// within the given range. Returns the port and a range [port, port+rangeSize-1].
func allocFreeRange(t *testing.T, rangeSize uint) (minPort, maxPort uint) {
	t.Helper()
	for p := uint(19000); p < 20000; p++ {
		allFree := true
		for i := uint(0); i < rangeSize; i++ {
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+i))
			if err != nil {
				allFree = false
				break
			}
			l.Close()
		}
		if allFree {
			return p, p + rangeSize - 1
		}
	}
	t.Skip("cannot find free port range on 127.0.0.1")
	return 0, 0
}

func TestPortRegistry_Allocate_SkipsS1Ports(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 5)
	s1Port := minPort

	pc := readyPortCache(t, struct {
		res  string
		ip   string
		port uint
	}{"r0", "127.0.0.1", s1Port})
	cl := buildPortRegistryClient(t)
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	port, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if port == s1Port {
		t.Errorf("should have skipped port %d (in S1)", s1Port)
	}
	if port < minPort || port > maxPort {
		t.Errorf("port %d outside range %d-%d", port, minPort, maxPort)
	}
}

func TestPortRegistry_Allocate_SkipsS2Ports(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 5)
	s2Port := minPort

	pc := readyPortCache(t)
	cl := buildPortRegistryClient(t, drbdResourceWithPort("r1", portRegistryTestNode, "127.0.0.1", s2Port))
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	port, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if port == s2Port {
		t.Errorf("should have skipped port %d (in S2)", s2Port)
	}
}

func TestPortRegistry_Allocate_SkipsPending(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 5)

	pc := readyPortCache(t)
	cl := buildPortRegistryClient(t)
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	port1, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	port2, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	if port1 == port2 {
		t.Errorf("two sequential allocations returned the same port %d", port1)
	}
}

func TestPortRegistry_Allocate_PendingTTLExpiry(t *testing.T) {
	minPort, _ := allocFreeRange(t, 2)

	pc := readyPortCache(t)
	cl := buildPortRegistryClient(t)
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, minPort, time.Millisecond)

	port1, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Millisecond)

	port2, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatalf("second allocation after TTL should succeed: %v", err)
	}

	if port1 != port2 {
		t.Errorf("after TTL expiry, expected same port %d but got %d", port1, port2)
	}
}

func TestPortRegistry_Allocate_SkipsOccupiedOS(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 5)

	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", minPort))
	if err != nil {
		t.Skipf("cannot bind 127.0.0.1:%d: %v", minPort, err)
	}
	defer l.Close()

	pc := readyPortCache(t)
	cl := buildPortRegistryClient(t)
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	port, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if port == minPort {
		t.Errorf("should have skipped port %d (occupied by OS listener)", minPort)
	}
}

func TestPortRegistry_Allocate_NoPortsAvailable(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 3)

	pc := readyPortCache(t,
		struct {
			res  string
			ip   string
			port uint
		}{"r0", "127.0.0.1", minPort},
		struct {
			res  string
			ip   string
			port uint
		}{"r1", "127.0.0.1", minPort + 1},
		struct {
			res  string
			ip   string
			port uint
		}{"r2", "127.0.0.1", minPort + 2},
	)
	cl := buildPortRegistryClient(t)
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	_, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err == nil {
		t.Fatal("expected error when no ports available")
	}
}

func TestPortRegistry_Allocate_ContextCancelled(t *testing.T) {
	pc := drbdr.NewDRBDPortCache()

	cl := buildPortRegistryClient(t)
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, 19000, 19005, 10*time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := reg.Allocate(ctx, "127.0.0.1")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestPortRegistry_Allocate_IgnoresOtherNodePorts(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 5)

	pc := readyPortCache(t)
	cl := buildPortRegistryClient(t, drbdResourceWithPort("r-other", "other-node", "127.0.0.1", minPort))
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	port, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if port != minPort {
		t.Errorf("expected port %d (other-node's port should be ignored by field index), got %d", minPort, port)
	}
}

func TestPortRegistry_Allocate_IgnoresOtherIPPorts(t *testing.T) {
	minPort, maxPort := allocFreeRange(t, 5)

	pc := readyPortCache(t)
	cl := buildPortRegistryClient(t, drbdResourceWithPort("r0", portRegistryTestNode, "10.0.0.1", minPort))
	reg := drbdr.NewPortRegistry(cl, portRegistryTestNode, pc, minPort, maxPort, 10*time.Minute)

	port, err := reg.Allocate(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if port != minPort {
		t.Errorf("expected port %d (different IP's port should not conflict), got %d", minPort, port)
	}
}
