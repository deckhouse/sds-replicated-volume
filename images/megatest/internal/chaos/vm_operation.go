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

// VirtualMachineOperation GVR
var vmOpGVR = schema.GroupVersionResource{
	Group:    "virtualization.deckhouse.io",
	Version:  "v1alpha2",
	Resource: "virtualmachineoperations",
}

// VMOperationManager manages VirtualMachineOperation for chaos scenarios
type VMOperationManager struct {
	cl          client.Client
	vmNamespace string
}

// NewVMOperationManager creates a new VMOperationManager
func NewVMOperationManager(cl client.Client, vmNamespace string) *VMOperationManager {
	return &VMOperationManager{
		cl:          cl,
		vmNamespace: vmNamespace,
	}
}

// CreateVMOperation creates a VirtualMachineOperation to control VM state
func (m *VMOperationManager) CreateVMOperation(ctx context.Context, vmName string, opType VMOperationType, force bool) error {
	opName := fmt.Sprintf("chaos-%s-%s-%d", string(opType), vmName, time.Now().Unix())

	vmOp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "virtualization.deckhouse.io/v1alpha2",
			"kind":       "VirtualMachineOperation",
			"metadata": map[string]interface{}{
				"name":      opName,
				"namespace": m.vmNamespace,
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

	if err := m.cl.Create(ctx, vmOp); err != nil {
		return fmt.Errorf("creating VMOperation %s for VM %s: %w", opName, vmName, err)
	}

	return nil
}

// ListVMOperationsForVM returns all VirtualMachineOperations for a specific VM
func (m *VMOperationManager) ListVMOperationsForVM(ctx context.Context, vmName string) ([]unstructured.Unstructured, error) {
	vmOpList := &unstructured.UnstructuredList{}
	vmOpList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vmOpGVR.Group,
		Version: vmOpGVR.Version,
		Kind:    "VirtualMachineOperationList",
	})

	if err := m.cl.List(ctx, vmOpList, client.InNamespace(m.vmNamespace), client.MatchingLabels{
		LabelChaosType:  string(ChaosTypeVMReboot),
		LabelChaosNodeA: vmName,
	}); err != nil {
		return nil, fmt.Errorf("listing VMOperations for VM %s: %w", vmName, err)
	}

	return vmOpList.Items, nil
}

// HasUnfinishedVMOperations checks if there are unfinished VirtualMachineOperations for a VM
// Returns true if there are operations with status.phase != Failed && != Completed
func (m *VMOperationManager) HasUnfinishedVMOperations(ctx context.Context, vmName string) (bool, error) {
	vmOps, err := m.ListVMOperationsForVM(ctx, vmName)
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

// CleanupAllVMOperations deletes all VirtualMachineOperations created by chaos
func (m *VMOperationManager) CleanupAllVMOperations(ctx context.Context) error {
	_, err := m.cleanupVMOperationsByLabel(ctx)
	return err
}

// CleanupStaleVMOperations cleans up any leftover VMOperations from previous runs
// Should be called at startup. Returns number of deleted operations.
func (m *VMOperationManager) CleanupStaleVMOperations(ctx context.Context) (int, error) {
	return m.cleanupVMOperationsByLabel(ctx)
}

func (m *VMOperationManager) cleanupVMOperationsByLabel(ctx context.Context) (int, error) {
	vmOpList := &unstructured.UnstructuredList{}
	vmOpList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vmOpGVR.Group,
		Version: vmOpGVR.Version,
		Kind:    "VirtualMachineOperationList",
	})

	if err := m.cl.List(ctx, vmOpList, client.InNamespace(m.vmNamespace), client.MatchingLabels{
		LabelChaosType: string(ChaosTypeVMReboot),
	}); err != nil {
		return 0, fmt.Errorf("listing VMOperations: %w", err)
	}

	deleted := 0
	for _, vmOp := range vmOpList.Items {
		if err := m.cl.Delete(ctx, &vmOp); err == nil {
			deleted++
		}
		// Ignore errors, best effort cleanup
	}

	return deleted, nil
}
