/*
Copyright 2025 Flant JSC

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

package rvrstatusconditions

import (
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	indextest "github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestAgentPodToRVRMapper(t *testing.T) {
	// Setup scheme
	s := scheme.Scheme
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}

	tests := []struct {
		name      string
		objects   []client.Object
		inputObj  client.Object
		wantNil   bool
		wantEmpty bool
		wantNames []string
	}{
		{
			name:     "non-Pod object returns nil",
			objects:  nil,
			inputObj: &v1alpha1.ReplicatedVolumeReplica{},
			wantNil:  true,
		},
		{
			name:    "pod in wrong namespace returns nil",
			objects: nil,
			inputObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-pod",
					Namespace: "wrong-namespace",
					Labels:    map[string]string{AgentPodLabel: AgentPodValue},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			wantNil: true,
		},
		{
			name:    "pod without agent label returns nil",
			objects: nil,
			inputObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-pod",
					Namespace: agentNamespaceDefault,
					Labels:    map[string]string{"app": "other"},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			wantNil: true,
		},
		{
			name:    "agent pod without NodeName returns nil",
			objects: nil,
			inputObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-pod",
					Namespace: agentNamespaceDefault,
					Labels:    map[string]string{AgentPodLabel: AgentPodValue},
				},
			},
			wantNil: true,
		},
		{
			name: "no RVRs on node returns empty",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-other-node"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
				},
			},
			inputObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-pod",
					Namespace: agentNamespaceDefault,
					Labels:    map[string]string{AgentPodLabel: AgentPodValue},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			wantEmpty: true,
		},
		{
			name: "returns requests for RVRs on same node",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-other"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
				},
			},
			inputObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-pod",
					Namespace: agentNamespaceDefault,
					Labels:    map[string]string{AgentPodLabel: AgentPodValue},
				},
				Spec: corev1.PodSpec{NodeName: "node-1"},
			},
			wantNames: []string{"rvr-1", "rvr-2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			// Build client
			builder := indextest.WithRVRByNodeNameIndex(fake.NewClientBuilder().WithScheme(s))
			if len(tc.objects) > 0 {
				builder = builder.WithObjects(tc.objects...)
			}
			cl := builder.Build()

			// Create mapper
			mapper := AgentPodToRVRMapper(cl, logr.Discard())

			// Run mapper
			result := mapper(ctx, tc.inputObj)

			// Assert
			if tc.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if tc.wantEmpty {
				if len(result) != 0 {
					t.Errorf("expected empty, got %v", result)
				}
				return
			}

			if len(tc.wantNames) > 0 {
				if len(result) != len(tc.wantNames) {
					t.Errorf("expected %d requests, got %d", len(tc.wantNames), len(result))
					return
				}

				gotNames := make(map[string]bool)
				for _, req := range result {
					gotNames[req.Name] = true
				}

				for _, name := range tc.wantNames {
					if !gotNames[name] {
						t.Errorf("expected request for %q not found in %v", name, resultNames(result))
					}
				}
			}
		})
	}
}

func resultNames(reqs []reconcile.Request) []string {
	names := make([]string, len(reqs))
	for i, req := range reqs {
		names[i] = req.Name
	}
	return names
}
