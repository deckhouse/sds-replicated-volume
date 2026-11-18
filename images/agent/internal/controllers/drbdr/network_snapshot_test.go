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
	"encoding/json"
	"testing"
	"time"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	fakedrbdutils "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils/fake"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/ctrlexec"
)

func TestInternalFirstAddressWithoutSDN(t *testing.T) {
	g := NewWithT(t)
	resource := &v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{SystemNetworks: []string{"Internal"}}}
	node := &corev1.Node{Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
		{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
		{Type: corev1.NodeInternalIP, Address: "10.0.0.2"},
	}}}
	r := &Reconciler{}
	g.Expect(r.getNetworkSnapshot(context.Background(), resource).err).NotTo(HaveOccurred())
	g.Expect(computeIntendedIPs(resource, node, networkSnapshot{})).To(Equal([]IntendedIP{{SystemNetworkName: "Internal", IPv4: "10.0.0.1"}}))
	resource.Spec.SystemNetworks = []string{"storage", "External"}
	node.Status.Addresses = append(node.Status.Addresses,
		corev1.NodeAddress{Type: "storageIP", Address: "192.0.2.1"},
		corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "192.0.2.2"})
	g.Expect(computeIntendedIPs(resource, node, networkSnapshot{})).To(BeEmpty())
}

func TestUnusableNetworkSnapshot(t *testing.T) {
	for _, value := range []string{"", "not-an-ip", "127.0.0.1", "0.0.0.0", "224.0.0.1", "::1", "192.0.2.1"} {
		t.Run(value, func(t *testing.T) {
			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(sdn.AddToScheme(scheme)).To(Succeed())
			nns := &sdn.NodeNetworkStatus{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Finalizers: []string{"test"}}, Status: sdn.NodeNetworkStatusStatus{Addresses: []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: value}}}}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nns).Build()
			r := &Reconciler{cl: cl, nodeName: nns.Name, sdnAvailable: true}
			resource := &v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{SystemNetworks: []string{"storage"}}}
			snapshot := r.getNetworkSnapshot(t.Context(), resource)
			if value == "192.0.2.1" {
				g.Expect(snapshot.err).NotTo(HaveOccurred())
			} else {
				g.Expect(snapshot.err).To(HaveOccurred())
				g.Expect(snapshot.addresses).To(BeEmpty())
			}
			g.Expect(cl.Delete(t.Context(), nns)).To(Succeed())
			snapshot = r.getNetworkSnapshot(t.Context(), resource)
			g.Expect(snapshot.err).To(HaveOccurred())
			g.Expect(snapshot.addresses).To(BeEmpty())
		})
	}
}

func TestFullReconcileSDNConditionRecovery(t *testing.T) {
	g := NewWithT(t)
	previousSymlinkDir := deviceSymlinkDir
	OverrideDeviceSymlinkDir(t.TempDir() + "/")
	t.Cleanup(func() { OverrideDeviceSymlinkDir(previousSymlinkDir) })
	drbdutils.ResetNextDeviceMinor()
	t.Cleanup(drbdutils.ResetNextDeviceMinor)
	ctx := t.Context()
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(sdn.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	resource := &v1alpha1.DRBDResource{ObjectMeta: metav1.ObjectMeta{Name: "resource-a", Finalizers: []string{v1alpha1.AgentFinalizer}}, Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-a", State: v1alpha1.DRBDResourceStateUp, Type: v1alpha1.DRBDResourceTypeDiskless, SystemNetworks: []string{"storage"}}}
	nns := &sdn.NodeNetworkStatus{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(resource).WithObjects(resource, nns, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}).
		WithIndex(resource, indexes.IndexFieldDRBDRByNodeName, func(client.Object) []string { return []string{"node-a"} }).Build()
	name := DRBDNameFromK8SName(resource.Name)
	observed := drbdutils.StatusResult{{Name: name, Role: "Secondary", Devices: []drbdutils.Device{{Volume: 0, Minor: 1000, DiskState: "Diskless"}}, Connections: []drbdutils.Connection{{PeerNodeID: 1, Paths: []drbdutils.Path{{ThisHost: drbdutils.Host{Address: "192.0.2.1", Port: 7000}}}}}}}
	statusJSON, err := json.Marshal(observed)
	g.Expect(err).NotTo(HaveOccurred())
	showJSON, err := json.Marshal([]drbdutils.ShowResource{{Resource: name}})
	g.Expect(err).NotTo(HaveOccurred())
	previous := drbdutils.ExecCommandContext
	t.Cleanup(func() { drbdutils.ExecCommandContext = previous })
	drbdutils.ExecCommandContext = func(_ context.Context, command string, args ...string) ctrlexec.Cmd {
		result := &fakedrbdutils.ExpectedCmd{Name: command, Args: args}
		if len(args) > 0 && args[0] == "status" {
			result.ResultOutput = statusJSON
		}
		if len(args) > 0 && args[0] == "show" {
			result.ResultOutput = showJSON
		}
		return result
	}
	ports := NewDRBDPortCache()
	ports.BeginDump()
	ports.EndDump()
	caches := NewCaches()
	caches.PopulateStatusForTest(observed)
	r := NewReconciler(cl, "node-a", NewPortRegistry(cl, "node-a", ports, 7000, 7999, time.Minute), caches)
	r.sdnAvailable = true
	for _, ip := range []string{"", "192.0.2.1", "invalid", "192.0.2.1"} {
		nns.Status.Addresses = []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: ip}}
		g.Expect(cl.Update(ctx, nns)).To(Succeed())
		_, reconcileErr := r.Reconcile(ctx, DRBDReconcileRequest{Name: resource.Name})
		g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(resource), resource)).To(Succeed())
		condition := meta.FindStatusCondition(resource.Status.Conditions, v1alpha1.DRBDResourceCondConfiguredType)
		g.Expect(condition).NotTo(BeNil())
		if ip == "192.0.2.1" {
			g.Expect(reconcileErr).NotTo(HaveOccurred())
			g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(condition.Reason).To(Equal(v1alpha1.DRBDResourceCondConfiguredReasonConfigured))
			g.Expect(resource.Status.Addresses).To(HaveLen(1))
		} else {
			g.Expect(reconcileErr).To(HaveOccurred())
			g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(condition.Reason).To(Equal(v1alpha1.DRBDResourceCondConfiguredReasonSystemNetworkAddressUnavailable))
			g.Expect(resource.Status.Addresses).To(BeEmpty())
		}
	}
}

func TestNetworkSnapshotExplicitReconcile(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	scheme := runtime.NewScheme()
	g.Expect(sdn.AddToScheme(scheme)).To(Succeed())
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	nns := &sdn.NodeNetworkStatus{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nns).Build()
	r := &Reconciler{cl: cl, nodeName: "node-a", sdnAvailable: true}
	resource := &v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{SystemNetworks: []string{"storage"}}}
	allocator := func(context.Context, string) (uint, error) { return 7000, nil }
	for _, ip := range []string{"", "192.0.2.1", "192.0.2.2", "", "192.0.2.3"} {
		nns.Status.Addresses = []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: ip}}
		g.Expect(cl.Update(ctx, nns)).To(Succeed())
		snapshot := r.getNetworkSnapshot(ctx, resource)
		applyConfiguredCondition(resource, snapshot.err, false)
		g.Expect(ensureAddresses(ctx, resource, &corev1.Node{}, nil, allocator, snapshot).Error()).NotTo(HaveOccurred())
		if ip == "" {
			g.Expect(resource.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
			g.Expect(snapshot.err).To(HaveOccurred())
			g.Expect(getConfiguredReason(snapshot.err)).To(Equal(v1alpha1.DRBDResourceCondConfiguredReasonSystemNetworkAddressUnavailable))
			g.Expect(resource.Status.Addresses).To(BeEmpty())
		} else {
			g.Expect(resource.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
			g.Expect(snapshot.err).NotTo(HaveOccurred())
			g.Expect(resource.Status.Addresses).To(HaveLen(1))
			g.Expect(resource.Status.Addresses[0].Address).To(Equal(v1alpha1.DRBDAddress{IPv4: ip, Port: 7000}))
		}
	}
	g.Expect(cl.Delete(ctx, nns)).To(Succeed())
	g.Expect(r.getNetworkSnapshot(ctx, resource).err).To(HaveOccurred())
	r.sdnAvailable = false
	r.cl = nil // Disabled SDN must never attempt an implicit cache read.
	g.Expect(r.getNetworkSnapshot(ctx, resource).err).To(HaveOccurred())
	resource.Spec.State = v1alpha1.DRBDResourceStateDown
	g.Expect(r.getNetworkSnapshot(ctx, resource).err).NotTo(HaveOccurred())
}

func TestReconcileSDNAddresses(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	scheme := runtime.NewScheme()
	g.Expect(sdn.AddToScheme(scheme)).To(Succeed())
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
	nns := &sdn.NodeNetworkStatus{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
	resource := &v1alpha1.DRBDResource{ObjectMeta: metav1.ObjectMeta{Name: "resource-a"}, Spec: v1alpha1.DRBDResourceSpec{SystemNetworks: []string{"storage"}}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(resource).WithObjects(node, nns, resource).Build()
	r := &Reconciler{cl: cl, nodeName: node.Name, sdnAvailable: true, portRegistry: &PortRegistry{}}
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "", "192.0.2.3"} {
		nns.Status.Addresses = []sdn.NodeNetworkStatusAddress{{Type: sdn.NodeNetworkStatusAddressTypeSystemNetworkIP, SystemNetworkName: "storage", Address: ip}}
		g.Expect(cl.Update(ctx, nns)).To(Succeed())
		outcome, diagnostic := r.reconcileAddresses(ctx, resource, ExistingPorts{ip: 7000})
		g.Expect(outcome.Error()).NotTo(HaveOccurred())
		applyConfiguredCondition(resource, diagnostic, false)
		if ip == "" {
			g.Expect(diagnostic).To(HaveOccurred())
			g.Expect(resource.Status.Addresses).To(BeEmpty())
			g.Expect(resource.Status.Conditions[0].Reason).To(Equal(v1alpha1.DRBDResourceCondConfiguredReasonSystemNetworkAddressUnavailable))
		} else {
			g.Expect(diagnostic).NotTo(HaveOccurred())
			g.Expect(resource.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
			g.Expect(resource.Status.Addresses[0].Address.IPv4).To(Equal(ip))
			g.Expect(resource.Status.Addresses[0].Address.Port).To(Equal(uint(7000)))
		}
		persisted := &v1alpha1.DRBDResource{}
		g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(resource), persisted)).To(Succeed())
		g.Expect(persisted.Status.Addresses).To(Equal(resource.Status.Addresses))
	}
}
