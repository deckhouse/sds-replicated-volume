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

package chaos

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CiliumNetworkPolicy GVR
var cnpGVR = schema.GroupVersionResource{
	Group:    "cilium.io",
	Version:  "v2",
	Resource: "ciliumnetworkpolicies",
}

// CiliumPolicyManager manages CiliumNetworkPolicy for chaos scenarios
type CiliumPolicyManager struct {
	cl client.Client
}

// NewCiliumPolicyManager creates a new CiliumPolicyManager
func NewCiliumPolicyManager(cl client.Client) *CiliumPolicyManager {
	return &CiliumPolicyManager{cl: cl}
}

// BlockAllNetwork creates a CiliumNetworkPolicy to block all network between two nodes
// Returns the policy name for later cleanup
func (m *CiliumPolicyManager) BlockAllNetwork(ctx context.Context, nodeA, nodeB NodeInfo, namespace string) (string, error) {
	policyName := fmt.Sprintf("chaos-net-%s-%s-%d", nodeA.Name, nodeB.Name, time.Now().Unix())

	policy := m.buildNetworkBlockPolicy(policyName, nodeA, nodeB, namespace)

	if err := m.cl.Create(ctx, policy); err != nil {
		return "", fmt.Errorf("creating network block policy %s: %w", policyName, err)
	}

	return policyName, nil
}

// UnblockTraffic deletes a CiliumNetworkPolicy by name
func (m *CiliumPolicyManager) UnblockTraffic(ctx context.Context, policyName string, namespace string) error {
	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   cnpGVR.Group,
		Version: cnpGVR.Version,
		Kind:    "CiliumNetworkPolicy",
	})
	policy.SetName(policyName)
	policy.SetNamespace(namespace)

	if err := m.cl.Delete(ctx, policy); err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}

// CleanupAllChaosPolicies deletes all CiliumNetworkPolicy created by chaos
func (m *CiliumPolicyManager) CleanupAllChaosPolicies(ctx context.Context, namespace string) error {
	deleted, err := m.cleanupPoliciesByLabel(ctx, namespace)
	if err != nil {
		return err
	}
	_ = deleted // unused but returned for logging purposes
	return nil
}

// CleanupStaleChaosPolicies cleans up any leftover Cilium policies from previous runs
// Should be called at startup. Returns number of deleted policies.
func (m *CiliumPolicyManager) CleanupStaleChaosPolicies(ctx context.Context, namespace string) (int, error) {
	return m.cleanupPoliciesByLabel(ctx, namespace)
}

func (m *CiliumPolicyManager) cleanupPoliciesByLabel(ctx context.Context, namespace string) (int, error) {
	policyList := &unstructured.UnstructuredList{}
	policyList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   cnpGVR.Group,
		Version: cnpGVR.Version,
		Kind:    "CiliumNetworkPolicyList",
	})

	if err := m.cl.List(ctx, policyList, client.InNamespace(namespace)); err != nil {
		return 0, fmt.Errorf("listing Cilium policies: %w", err)
	}

	deleted := 0
	for _, policy := range policyList.Items {
		labels := policy.GetLabels()
		if labels == nil {
			continue
		}

		// Only delete policies created by chaos (have chaos.megatest/type label)
		if _, ok := labels[LabelChaosType]; ok {
			if err := m.cl.Delete(ctx, &policy); err == nil {
				deleted++
			}
			// Ignore errors, best effort cleanup
		}
	}

	return deleted, nil
}

// buildNetworkBlockPolicy creates a CiliumNetworkPolicy to block all network
func (m *CiliumPolicyManager) buildNetworkBlockPolicy(name string, nodeA, nodeB NodeInfo, namespace string) *unstructured.Unstructured {
	policy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					LabelChaosType:  string(ChaosTypeNetworkBlock),
					LabelChaosNodeA: nodeA.Name,
					LabelChaosNodeB: nodeB.Name,
				},
			},
			"spec": map[string]interface{}{
				"endpointSelector": map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      "vm.kubevirt.internal.virtualization.deckhouse.io/name",
							"operator": "In",
							"values":   []interface{}{nodeA.Name, nodeB.Name},
						},
					},
				},
				"ingress": []interface{}{
					map[string]interface{}{
						"fromEndpoints": []interface{}{
							map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"io.kubernetes.pod.namespace": namespace,
								},
							},
						},
					},
				},
				"egress": []interface{}{
					map[string]interface{}{
						"toEndpoints": []interface{}{
							map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"io.kubernetes.pod.namespace": namespace,
								},
							},
						},
					},
				},
				"ingressDeny": []interface{}{
					map[string]interface{}{
						"fromEndpoints": []interface{}{
							map[string]interface{}{
								"matchExpressions": []interface{}{
									map[string]interface{}{
										"key":      "vm.kubevirt.internal.virtualization.deckhouse.io/name",
										"operator": "In",
										"values":   []interface{}{nodeA.Name, nodeB.Name},
									},
								},
							},
						},
						"toPorts": []interface{}{
							map[string]interface{}{
								"ports": []interface{}{
									map[string]interface{}{
										"port":     "1",
										"endPort":  65535,
										"protocol": "TCP",
									},
								},
							},
						},
					},
				},
				"egressDeny": []interface{}{
					map[string]interface{}{
						"toEndpoints": []interface{}{
							map[string]interface{}{
								"matchExpressions": []interface{}{
									map[string]interface{}{
										"key":      "vm.kubevirt.internal.virtualization.deckhouse.io/name",
										"operator": "In",
										"values":   []interface{}{nodeA.Name, nodeB.Name},
									},
								},
							},
						},
						"toPorts": []interface{}{
							map[string]interface{}{
								"ports": []interface{}{
									map[string]interface{}{
										"port":     "1",
										"endPort":  65535,
										"protocol": "TCP",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return policy
}
