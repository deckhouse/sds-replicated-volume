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
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DVP VirtualMachine and VirtualMachineOperation GVR
var (
	vmGVR = schema.GroupVersionResource{
		Group:    "virtualization.deckhouse.io",
		Version:  "v1alpha2",
		Resource: "virtualmachines",
	}
	vmOpGVR = schema.GroupVersionResource{
		Group:    "virtualization.deckhouse.io",
		Version:  "v1alpha2",
		Resource: "virtualmachineoperations",
	}
)

// ParentClient wraps a Kubernetes client for the DVP parent cluster
type ParentClient struct {
	cl          client.Client
	vmNamespace string
}

// NewParentClient creates a new client for the DVP parent cluster
func NewParentClient(kubeconfigPath, vmNamespace string) (*ParentClient, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("building parent config from kubeconfig file %s: %w", kubeconfigPath, err)
	}

	// Disable rate limiter for chaos operations
	cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return &ParentClient{
		cl:          cl,
		vmNamespace: vmNamespace,
	}, nil
}

// NewParentClientFromConfig creates a new client from existing rest.Config
func NewParentClientFromConfig(cfg *rest.Config, vmNamespace string) (*ParentClient, error) {
	// Disable rate limiter for chaos operations
	cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("creating parent client: %w", err)
	}

	return &ParentClient{
		cl:          cl,
		vmNamespace: vmNamespace,
	}, nil
}

// ListVMs returns all VirtualMachines with their IPs
func (c *ParentClient) ListVMs(ctx context.Context) ([]NodeInfo, error) {
	vmList := &unstructured.UnstructuredList{}
	vmList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vmGVR.Group,
		Version: vmGVR.Version,
		Kind:    "VirtualMachineList",
	})

	if err := c.cl.List(ctx, vmList, client.InNamespace(c.vmNamespace)); err != nil {
		return nil, fmt.Errorf("listing VMs in namespace %s: %w", c.vmNamespace, err)
	}

	var nodes []NodeInfo
	for _, vm := range vmList.Items {
		name := vm.GetName()

		// Get IP from status.ipAddress
		ipAddress, found, err := unstructured.NestedString(vm.Object, "status", "ipAddress")
		if err != nil || !found || ipAddress == "" {
			slog.Debug("skipping VM without IP address", "vm", name, "namespace", c.vmNamespace)
			continue
		}

		// Check phase is Running
		phase, _, _ := unstructured.NestedString(vm.Object, "status", "phase")
		if phase != "Running" {
			slog.Debug("skipping VM not in Running phase", "vm", name, "phase", phase)
			continue
		}

		nodes = append(nodes, NodeInfo{
			Name:      name,
			IPAddress: ipAddress,
		})
	}

	return nodes, nil
}

// CreateVMOperation creates a VirtualMachineOperation to control VM state
func (c *ParentClient) CreateVMOperation(ctx context.Context, vmName string, opType VMOperationType, force bool) error {
	opName := fmt.Sprintf("chaos-%s-%s-%d", string(opType), vmName, time.Now().Unix())

	vmOp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "virtualization.deckhouse.io/v1alpha2",
			"kind":       "VirtualMachineOperation",
			"metadata": map[string]interface{}{
				"name":      opName,
				"namespace": c.vmNamespace,
				"labels": map[string]interface{}{
					LabelChaosType:  string(ChaosTypeVMReboot),
					LabelChaosNodeA: vmName,
				},
			},
			"spec": map[string]interface{}{
				"virtualMachineName": vmName,
				"type":               string(opType),
				"force":              force,
			},
		},
	}

	if err := c.cl.Create(ctx, vmOp); err != nil {
		return fmt.Errorf("creating VMOperation %s for VM %s: %w", opName, vmName, err)
	}

	return nil
}

// CleanupVMOperations deletes all VirtualMachineOperations created by chaos
func (c *ParentClient) CleanupVMOperations(ctx context.Context) error {
	_, err := c.cleanupVMOperationsByLabel(ctx)
	return err
}

// CleanupStaleVMOperations cleans up any leftover VMOperations from previous runs
// Should be called at startup. Returns number of deleted operations.
func (c *ParentClient) CleanupStaleVMOperations(ctx context.Context) (int, error) {
	return c.cleanupVMOperationsByLabel(ctx)
}

func (c *ParentClient) cleanupVMOperationsByLabel(ctx context.Context) (int, error) {
	vmOpList := &unstructured.UnstructuredList{}
	vmOpList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vmOpGVR.Group,
		Version: vmOpGVR.Version,
		Kind:    "VirtualMachineOperationList",
	})

	if err := c.cl.List(ctx, vmOpList, client.InNamespace(c.vmNamespace), client.MatchingLabels{
		LabelChaosType: string(ChaosTypeVMReboot),
	}); err != nil {
		return 0, fmt.Errorf("listing VMOperations: %w", err)
	}

	deleted := 0
	for _, vmOp := range vmOpList.Items {
		if err := c.cl.Delete(ctx, &vmOp); err == nil {
			deleted++
		}
		// Ignore errors, best effort cleanup
	}

	return deleted, nil
}

// ListVMOperationsForVM returns all VirtualMachineOperations for a specific VM
func (c *ParentClient) ListVMOperationsForVM(ctx context.Context, vmName string) ([]unstructured.Unstructured, error) {
	vmOpList := &unstructured.UnstructuredList{}
	vmOpList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vmOpGVR.Group,
		Version: vmOpGVR.Version,
		Kind:    "VirtualMachineOperationList",
	})

	if err := c.cl.List(ctx, vmOpList, client.InNamespace(c.vmNamespace), client.MatchingLabels{
		LabelChaosType:  string(ChaosTypeVMReboot),
		LabelChaosNodeA: vmName,
	}); err != nil {
		return nil, fmt.Errorf("listing VMOperations for VM %s: %w", vmName, err)
	}

	return vmOpList.Items, nil
}

// HasUnfinishedVMOperations checks if there are unfinished VirtualMachineOperations for a VM
// Returns true if there are operations with status.phase != Failed && != Completed
func (c *ParentClient) HasUnfinishedVMOperations(ctx context.Context, vmName string) (bool, error) {
	vmOps, err := c.ListVMOperationsForVM(ctx, vmName)
	if err != nil {
		return false, err
	}

	if len(vmOps) == 0 {
		return false, nil
	}

	// Count operations with phase = Failed or Completed
	finishedCount := 0
	for _, vmOp := range vmOps {
		phase, found, err := unstructured.NestedString(vmOp.Object, "status", "phase")
		if err != nil {
			// If we can't read phase, assume it's unfinished
			return true, nil
		}
		if found && (phase == "Failed" || phase == "Completed") {
			finishedCount++
		}
	}

	// If count of finished operations != total count, there are unfinished operations
	return finishedCount != len(vmOps), nil
}
