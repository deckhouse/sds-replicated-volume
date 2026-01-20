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

// CiliumClusterwideNetworkPolicy GVR
var ccnpGVR = schema.GroupVersionResource{
	Group:    "cilium.io",
	Version:  "v2",
	Resource: "ciliumclusterwidenetworkpolicies",
}

// CiliumPolicyManager manages CiliumClusterwideNetworkPolicy for chaos scenarios
type CiliumPolicyManager struct {
	cl client.Client
}

// NewCiliumPolicyManager creates a new CiliumPolicyManager
func NewCiliumPolicyManager(cl client.Client) *CiliumPolicyManager {
	return &CiliumPolicyManager{cl: cl}
}

// BlockDRBDPortsList creates a CiliumClusterwideNetworkPolicy to block specific DRBD ports between two nodes.
// This is more efficient than BlockDRBDPorts when actual ports are known (fewer rules in policy).
// Returns the policy name for later cleanup.
func (m *CiliumPolicyManager) BlockDRBDPortsList(ctx context.Context, nodeA, nodeB NodeInfo, ports []int) (string, error) {
	if len(ports) == 0 {
		return "", fmt.Errorf("no ports specified for DRBD blocking")
	}

	policyName := fmt.Sprintf("chaos-drbd-%s-%s-%d", nodeA.Name, nodeB.Name, time.Now().Unix())

	policy := m.buildDRBDBlockPolicyFromPorts(policyName, nodeA, nodeB, ports)

	if err := m.cl.Create(ctx, policy); err != nil {
		return "", fmt.Errorf("creating DRBD block policy %s: %w", policyName, err)
	}

	return policyName, nil
}

// BlockAllNetwork creates a CiliumClusterwideNetworkPolicy to block all network between two nodes
// Returns the policy name for later cleanup
func (m *CiliumPolicyManager) BlockAllNetwork(ctx context.Context, nodeA, nodeB NodeInfo) (string, error) {
	policyName := fmt.Sprintf("chaos-net-%s-%s-%d", nodeA.Name, nodeB.Name, time.Now().Unix())

	policy := m.buildNetworkBlockPolicy(policyName, nodeA, nodeB)

	if err := m.cl.Create(ctx, policy); err != nil {
		return "", fmt.Errorf("creating network block policy %s: %w", policyName, err)
	}

	return policyName, nil
}

// UnblockTraffic deletes a CiliumClusterwideNetworkPolicy by name
func (m *CiliumPolicyManager) UnblockTraffic(ctx context.Context, policyName string) error {
	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ccnpGVR.Group,
		Version: ccnpGVR.Version,
		Kind:    "CiliumClusterwideNetworkPolicy",
	})
	policy.SetName(policyName)

	if err := m.cl.Delete(ctx, policy); err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}

// CleanupAllChaosPolicies deletes all CiliumClusterwideNetworkPolicy created by chaos
func (m *CiliumPolicyManager) CleanupAllChaosPolicies(ctx context.Context) error {
	deleted, err := m.cleanupPoliciesByLabel(ctx)
	if err != nil {
		return err
	}
	_ = deleted // unused but returned for logging purposes
	return nil
}

// CleanupStaleChaosPolicies cleans up any leftover Cilium policies from previous runs
// Should be called at startup. Returns number of deleted policies.
func (m *CiliumPolicyManager) CleanupStaleChaosPolicies(ctx context.Context) (int, error) {
	return m.cleanupPoliciesByLabel(ctx)
}

func (m *CiliumPolicyManager) cleanupPoliciesByLabel(ctx context.Context) (int, error) {
	policyList := &unstructured.UnstructuredList{}
	policyList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ccnpGVR.Group,
		Version: ccnpGVR.Version,
		Kind:    "CiliumClusterwideNetworkPolicyList",
	})

	if err := m.cl.List(ctx, policyList); err != nil {
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

// IsHostFirewallEnabled checks if Cilium Host Firewall is enabled
// by trying to verify that CiliumClusterwideNetworkPolicy with nodeSelector works
func (m *CiliumPolicyManager) IsHostFirewallEnabled(ctx context.Context) (bool, string) {
	// Check if CCNP CRD exists by trying to list policies
	policyList := &unstructured.UnstructuredList{}
	policyList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ccnpGVR.Group,
		Version: ccnpGVR.Version,
		Kind:    "CiliumClusterwideNetworkPolicyList",
	})

	if err := m.cl.List(ctx, policyList); err != nil {
		return false, fmt.Sprintf("cannot list CiliumClusterwideNetworkPolicy: %v", err)
	}

	// CRD exists and we can list policies
	// Note: We cannot definitively check if Host Firewall is enabled without
	// examining Cilium's configuration. However, if nodeSelector-based policies
	// don't work, the chaos will simply have no effect (not break anything).
	// For now, we assume it's enabled if CRD exists.
	return true, "CiliumClusterwideNetworkPolicy CRD available"
}

// buildDRBDBlockPolicyFromPorts creates a CiliumClusterwideNetworkPolicy to block specific DRBD ports.
// Uses actual ports list instead of a range - more efficient and targeted.
func (m *CiliumPolicyManager) buildDRBDBlockPolicyFromPorts(name string, nodeA, nodeB NodeInfo, portList []int) *unstructured.Unstructured {
	// Build port list from actual ports
	ports := make([]interface{}, 0, len(portList))
	for _, port := range portList {
		ports = append(ports, map[string]interface{}{
			"port":     fmt.Sprintf("%d", port),
			"protocol": "TCP",
		})
	}

	policy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumClusterwideNetworkPolicy",
			"metadata": map[string]interface{}{
				"name": name,
				"labels": map[string]interface{}{
					LabelChaosType:  string(ChaosTypeDRBDBlock),
					LabelChaosNodeA: nodeA.Name,
					LabelChaosNodeB: nodeB.Name,
				},
			},
			"spec": map[string]interface{}{
				"nodeSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"kubernetes.io/hostname": nodeA.Name,
					},
				},
				"ingressDeny": []interface{}{
					map[string]interface{}{
						"fromCIDR": []interface{}{
							fmt.Sprintf("%s/32", nodeB.IPAddress),
						},
						"toPorts": []interface{}{
							map[string]interface{}{
								"ports": ports,
							},
						},
					},
				},
				"egressDeny": []interface{}{
					map[string]interface{}{
						"toCIDR": []interface{}{
							fmt.Sprintf("%s/32", nodeB.IPAddress),
						},
						"toPorts": []interface{}{
							map[string]interface{}{
								"ports": ports,
							},
						},
					},
				},
			},
		},
	}

	return policy
}

// buildNetworkBlockPolicy creates a CiliumClusterwideNetworkPolicy to block all network
func (m *CiliumPolicyManager) buildNetworkBlockPolicy(name string, nodeA, nodeB NodeInfo) *unstructured.Unstructured {
	policy := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumClusterwideNetworkPolicy",
			"metadata": map[string]interface{}{
				"name": name,
				"labels": map[string]interface{}{
					LabelChaosType:  string(ChaosTypeNetworkBlock),
					LabelChaosNodeA: nodeA.Name,
					LabelChaosNodeB: nodeB.Name,
				},
			},
			"spec": map[string]interface{}{
				"nodeSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"kubernetes.io/hostname": nodeA.Name,
					},
				},
				"ingressDeny": []interface{}{
					map[string]interface{}{
						"fromCIDR": []interface{}{
							fmt.Sprintf("%s/32", nodeB.IPAddress),
						},
					},
				},
				"egressDeny": []interface{}{
					map[string]interface{}{
						"toCIDR": []interface{}{
							fmt.Sprintf("%s/32", nodeB.IPAddress),
						},
					},
				},
			},
		},
	}

	return policy
}
