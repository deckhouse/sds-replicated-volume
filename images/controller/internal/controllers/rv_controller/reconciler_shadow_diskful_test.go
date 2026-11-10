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

package rvcontroller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestComputeShadowDiskfulSupported(t *testing.T) {
	node := func(name string) corev1.Node {
		return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{v1alpha1.AgentNodeLabelKey: ""}}}
	}
	pod := func(node, support string) corev1.Pod {
		p := corev1.Pod{Spec: corev1.PodSpec{NodeName: node}}
		if support != "" {
			p.Annotations = map[string]string{v1alpha1.AgentShadowDiskfulSupportedAnnotationKey: support}
		}
		return p
	}
	terminating := pod("n1", "true")
	terminating.DeletionTimestamp = new(metav1.Now())
	failed := pod("n1", "")
	failed.Status.Phase = corev1.PodFailed
	succeeded := pod("n1", "")
	succeeded.Status.Phase = corev1.PodSucceeded
	notReady := pod("n1", "true")
	notReady.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
	for _, tc := range []struct {
		name  string
		nodes []corev1.Node
		pods  []corev1.Pod
		want  bool
	}{
		{name: "no nodes"},
		{name: "all true", nodes: []corev1.Node{node("n1"), node("n2")}, pods: []corev1.Pod{pod("n1", "true"), pod("n2", "true")}, want: true},
		{name: "node without Pod", nodes: []corev1.Node{node("n1"), node("n2")}, pods: []corev1.Pod{pod("n1", "true")}},
		{name: "old agent", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{pod("n1", "")}},
		{name: "false", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{pod("n1", "false")}},
		{name: "terminating old Pod and new without report", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{terminating, pod("n1", "")}},
		{name: "only terminating Pod", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{terminating}},
		{name: "Failed Pod beside live", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{failed, pod("n1", "true")}, want: true},
		{name: "Succeeded Pod beside live", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{succeeded, pod("n1", "true")}, want: true},
		{name: "only Failed Pod", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{failed}},
		{name: "Pod on non-agent node", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{pod("n1", "true"), pod("n2", "false")}, want: true},
		{name: "NotReady Pod reports true", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{notReady}, want: true},
		{name: "mixed live Pods", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{pod("n1", "true"), pod("n1", "false")}},
		{name: "two live supported Pods", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{pod("n1", "true"), pod("n1", "true")}, want: true},
		{name: "annotation is exact", nodes: []corev1.Node{node("n1")}, pods: []corev1.Pod{pod("n1", "True")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeShadowDiskfulSupported(tc.nodes, tc.pods); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}

func TestReconcileNormalOperationAgentReadErrors(t *testing.T) {
	for _, failOn := range []string{"nodes", "pods"} {
		t.Run(failOn, func(t *testing.T) {
			scheme := newScheme()
			readErr := errors.New("cache read failed")
			calls := 0
			cl := newClientBuilder(scheme).WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					calls++
					_, nodes := list.(*corev1.NodeList)
					if nodes == (failOn == "nodes") {
						return readErr
					}
					return cl.List(ctx, list, opts...)
				},
			}).Build()
			rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{},
			}}
			var rvrs []*v1alpha1.ReplicatedVolumeReplica
			var completed []v1alpha1.ReplicatedVolumeDatameshTransition
			outcome := NewReconciler(cl, scheme).reconcileNormalOperation(t.Context(), rv, &rvrs, nil, nil, &completed)
			if !errors.Is(outcome.Error(), readErr) {
				t.Fatalf("got error %v, want %v", outcome.Error(), readErr)
			}
			wantCalls := 1
			if failOn == "pods" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("List calls = %d, want %d", calls, wantCalls)
			}
		})
	}
}

var _ = Describe("agent ShadowDiskful capability in normal reconciliation", func() {
	DescribeTable("selects a path from the cached agent reports", func(ctx SpecContext, support, wantPlan string) {
		scheme := newScheme()
		rv := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv-1"}, Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal},
			DatameshRevision: 5,
			Datamesh:         v1alpha1.ReplicatedVolumeDatamesh{Quorum: 2, QuorumMinimumRedundancy: 1, SystemNetworkNames: []string{"default"}},
			DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{{Name: "rv-1-2", Request: v1alpha1.DatameshMembershipRequest{
				Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeDiskful, LVMVolumeGroupName: "lvg",
			}}},
		}}
		rsp := &rspView{SystemNetworkNames: []string{"default"}}
		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		var objects []client.Object
		for i := range 3 {
			name, node := fmt.Sprintf("rv-1-%d", i), fmt.Sprintf("n%d", i)
			rvr := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:   v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: node},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 5, Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}},
			}
			rvrs = append(rvrs, rvr)
			if i < 2 {
				rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{
					Name: name, Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: node, LVMVolumeGroupName: "lvg", Addresses: rvr.Status.Addresses,
				})
			}
			rsp.EligibleNodes = append(rsp.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: node})
			objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node, Labels: map[string]string{v1alpha1.AgentNodeLabelKey: ""}}},
				&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "agent-" + node, Namespace: "agents", Labels: map[string]string{"app": "agent"},
					Annotations: map[string]string{v1alpha1.AgentShadowDiskfulSupportedAnnotationKey: "true"}}, Spec: corev1.PodSpec{NodeName: node}},
			)
		}
		// One old/unsupported agent blocks the capability for the whole cluster.
		lastPod := objects[len(objects)-1].(*corev1.Pod)
		lastPod.Annotations = nil
		if support != "" {
			lastPod.Annotations = map[string]string{v1alpha1.AgentShadowDiskfulSupportedAnnotationKey: support}
		}
		// Model the manager cache: only app=agent Pods in the module namespace.
		cl := newClientBuilder(scheme).WithObjects(objects...).Build()
		rec := NewReconciler(cl, scheme)
		var completed []v1alpha1.ReplicatedVolumeDatameshTransition
		outcome := rec.reconcileNormalOperation(ctx, rv, &rvrs, nil, rsp, &completed)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal(wantPlan))
	},
		Entry("all agents support", "true", "diskful-via-sd/v1"),
		Entry("old agent", "", "diskful/v1"),
		Entry("unsupported agent", "false", "diskful/v1"),
	)
})
