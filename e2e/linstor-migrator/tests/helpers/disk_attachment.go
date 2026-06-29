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

package helpers

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sncv1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	e2ekubernetes "github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// diskAttachmentConfig holds configuration for attaching disks to cluster nodes.
type diskAttachmentConfig struct {
	// RunID is the unique identifier for this test run.
	RunID string
	// DiskSize is the size of each disk to attach (e.g., "30Gi").
	DiskSize string
	// StorageClassName is the StorageClass to use for VirtualDisks.
	StorageClassName string
	// Namespace is the namespace for VirtualDisk resources.
	Namespace string
}

// attachedDiskResult holds information about a successfully attached disk.
type attachedDiskResult struct {
	NodeName       string
	DiskName       string
	AttachmentName string
	SerialNumber   string
	BlockDevice    *sncv1.BlockDevice
}

// attachDisksToAllNodes attaches one disk of the specified size to each node
// in the cluster. It uses VirtualDisks on the base cluster (if available) and
// waits for BlockDevices to appear.
func attachDisksToAllNodes(
	ctx context.Context,
	baseKubeconfig *rest.Config,
	testClusterRes *cluster.TestClusterResources,
	k8sClient client.Client,
	config diskAttachmentConfig,
) ([]*attachedDiskResult, error) {
	slog.Debug(fmt.Sprintf("CHECK: Attaching %s disks to all nodes", config.DiskSize))

	// Get the list of nodes in the test cluster
	var nodeList corev1.NodeList
	if err := k8sClient.List(ctx, &nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	if len(nodeList.Items) == 0 {
		return nil, fmt.Errorf("no nodes found in the cluster")
	}

	nodeNames := make([]string, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		nodeNames = append(nodeNames, node.Name)
	}

	slog.Info(fmt.Sprintf("Found %d nodes: %v", len(nodeNames), nodeNames))

	// If we have access to base cluster (for VirtualDisks), use that approach
	if baseKubeconfig != nil {
		vmResources, err := resolveVMResourcesForAttachment(ctx, baseKubeconfig, testClusterRes, config.Namespace)
		if err != nil {
			return nil, fmt.Errorf("base cluster kubeconfig is available, but failed to resolve VM list for VirtualDisk attachment: %w", err)
		}
		return attachDisksViaVirtualDisks(ctx, baseKubeconfig, vmResources, nodeNames, k8sClient, config)
	}

	// Otherwise, assume disks are already attached (manual setup)
	slog.Warn("Base kubeconfig is nil, assuming disks are already attached")
	return findExistingBlockDevices(ctx, k8sClient, nodeNames)
}

// attachDisksViaVirtualDisks creates VirtualDisks on the base cluster, waits for
// attachments/BlockDevices readiness, and returns attached disk results.
func attachDisksViaVirtualDisks(
	ctx context.Context,
	baseKubeconfig *rest.Config,
	vmResources *cluster.VMResources,
	nodeNames []string,
	k8sClient client.Client,
	config diskAttachmentConfig,
) ([]*attachedDiskResult, error) {
	slog.Info("Attaching disks via VirtualDisks on base cluster")

	// Prefer VMs matching node names; this avoids attaching disks to setup/aux VMs.
	nodeNameSet := make(map[string]struct{}, len(nodeNames))
	for _, name := range nodeNames {
		nodeNameSet[name] = struct{}{}
	}

	vmNames := make([]string, 0, len(vmResources.VMNames))
	for _, name := range vmResources.VMNames {
		if _, ok := nodeNameSet[name]; ok {
			vmNames = append(vmNames, name)
		}
	}
	if len(vmNames) == 0 {
		// Fallback for environments where VM names do not exactly match Kubernetes node names.
		for _, name := range vmResources.VMNames {
			if name != vmResources.SetupVMName {
				vmNames = append(vmNames, name)
			}
		}
	}

	if len(vmNames) == 0 {
		return nil, fmt.Errorf("no VM names available for disk attachment")
	}

	// Create and attach VirtualDisks
	attachments := make([]*e2ekubernetes.VirtualDiskAttachmentResult, 0, len(vmNames))
	results := make([]*attachedDiskResult, 0, len(vmNames))
	expectedSerials := make(map[string]string) // serial -> disk name

	for i, vmName := range vmNames {
		diskName := fmt.Sprintf("%s-%s-%d", virtualDiskPrefix, config.RunID, i)

		slog.Info(fmt.Sprintf("Creating VirtualDisk %s for VM %s", diskName, vmName))

		att, err := e2ekubernetes.AttachVirtualDiskToVM(ctx, baseKubeconfig, e2ekubernetes.VirtualDiskAttachmentConfig{
			VMName:           vmName,
			Namespace:        vmResources.Namespace,
			DiskName:         diskName,
			DiskSize:         config.DiskSize,
			StorageClassName: config.StorageClassName,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to attach VirtualDisk %s to VM %s: %w", diskName, vmName, err)
		}

		attachments = append(attachments, att)
	}

	// Wait for VirtualDisk attachments to be ready
	attachCtx, cancel := context.WithTimeout(ctx, defaultWaitTimeout)
	defer cancel()

	for _, att := range attachments {
		slog.Info(fmt.Sprintf("Waiting for VirtualDiskAttachment %s to be ready", att.AttachmentName))
		if err := e2ekubernetes.WaitForVirtualDiskAttached(attachCtx, baseKubeconfig, vmResources.Namespace, att.AttachmentName, 10*time.Second); err != nil {
			return nil, fmt.Errorf("failed waiting for VirtualDiskAttachment %s: %w", att.AttachmentName, err)
		}
	}

	// Serial discovery in virtualization can use either VirtualDisk UID or
	// VirtualMachineBlockDeviceAttachment UID; accept both (same approach as SNC e2e).
	dynClient, err := dynamic.NewForConfig(baseKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client for base cluster: %w", err)
	}
	vdGVR := schema.GroupVersionResource{
		Group:    "virtualization.deckhouse.io",
		Version:  "v1alpha2",
		Resource: "virtualdisks",
	}
	vmbdaGVR := schema.GroupVersionResource{
		Group:    "virtualization.deckhouse.io",
		Version:  "v1alpha2",
		Resource: "virtualmachineblockdeviceattachments",
	}
	for _, att := range attachments {
		vdObj, err := dynClient.Resource(vdGVR).Namespace(vmResources.Namespace).Get(ctx, att.DiskName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get VirtualDisk %s for UID: %w", att.DiskName, err)
		}
		vmbdaObj, err := dynClient.Resource(vmbdaGVR).Namespace(vmResources.Namespace).Get(ctx, att.AttachmentName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get VirtualMachineBlockDeviceAttachment %s for UID: %w", att.AttachmentName, err)
		}

		serialVD := calculateBlockDeviceSerial(string(vdObj.GetUID()))
		serialVMBDA := calculateBlockDeviceSerial(string(vmbdaObj.GetUID()))
		expectedSerials[serialVD] = att.DiskName
		expectedSerials[serialVMBDA] = att.DiskName

		slog.Info(fmt.Sprintf("VirtualDisk %s expected serials: vd=%s, vmbda=%s", att.DiskName, serialVD, serialVMBDA))
	}

	// Wait for BlockDevices to appear with matching serials
	slog.Info(fmt.Sprintf("Waiting for BlockDevices to appear (timeout: %v)", blockDeviceWaitTimeout))

	var foundBlockDevices []*sncv1.BlockDevice
	deadline := time.Now().Add(blockDeviceWaitTimeout)
	for time.Now().Before(deadline) {
		var bdList sncv1.BlockDeviceList
		if err := k8sClient.List(ctx, &bdList); err != nil {
			slog.Warn(fmt.Sprintf("Failed to list BlockDevices: %v", err))
			time.Sleep(pollingInterval)
			continue
		}

		foundBlockDevices = nil
		for i := range bdList.Items {
			bd := &bdList.Items[i]
			// Check if this BD matches one of our expected serials
			actualSerial := strings.TrimSpace(bd.Status.Serial)
			for expectedSerial := range expectedSerials {
				if actualSerial == expectedSerial && bd.Status.Consumable {
					foundBlockDevices = append(foundBlockDevices, bd)
					break
				}
			}
		}

		if len(foundBlockDevices) >= len(vmNames) {
			break
		}

		slog.Info(fmt.Sprintf("Found %d/%d consumable BlockDevices, waiting...", len(foundBlockDevices), len(vmNames)))
		time.Sleep(pollingInterval)
	}

	if len(foundBlockDevices) < len(vmNames) {
		return nil, fmt.Errorf("timeout waiting for BlockDevices: found %d, expected %d", len(foundBlockDevices), len(vmNames))
	}

	// Build results
	for _, bd := range foundBlockDevices {
		result := &attachedDiskResult{
			NodeName:     bd.Status.NodeName,
			SerialNumber: bd.Status.Serial,
			BlockDevice:  bd,
		}
		results = append(results, result)
		slog.Info(fmt.Sprintf("BlockDevice %s found on node %s (serial: %s)", bd.Name, bd.Status.NodeName, bd.Status.Serial))
	}

	slog.Debug(fmt.Sprintf("PASS: Successfully attached disks to %d nodes", len(results)))
	return results, nil
}

// findExistingBlockDevices finds existing BlockDevices that are already attached to nodes.
// This is used when VirtualDisk attachment is not available.
func findExistingBlockDevices(ctx context.Context, k8sClient client.Client, nodeNames []string) ([]*attachedDiskResult, error) {
	slog.Info("Looking for existing BlockDevices on nodes")

	var bdList sncv1.BlockDeviceList
	if err := k8sClient.List(ctx, &bdList); err != nil {
		return nil, fmt.Errorf("failed to list BlockDevices: %w", err)
	}

	results := make([]*attachedDiskResult, 0, len(nodeNames))
	nodeFound := make(map[string]bool)

	for i := range bdList.Items {
		bd := &bdList.Items[i]
		if !bd.Status.Consumable {
			continue
		}

		for _, nodeName := range nodeNames {
			if bd.Status.NodeName == nodeName && !nodeFound[nodeName] {
				results = append(results, &attachedDiskResult{
					NodeName:     nodeName,
					SerialNumber: bd.Status.Serial,
					BlockDevice:  bd,
				})
				nodeFound[nodeName] = true
				break
			}
		}
	}

	if len(results) < len(nodeNames) {
		return nil, fmt.Errorf("found only %d/%d usable BlockDevices, expected one per node", len(results), len(nodeNames))
	}

	slog.Debug(fmt.Sprintf("PASS: Found %d existing BlockDevices", len(results)))
	return results, nil
}

// calculateBlockDeviceSerial calculates the expected BlockDevice serial from a VirtualDisk UID.
func calculateBlockDeviceSerial(vdUID string) string {
	h := md5.Sum([]byte(vdUID))
	return hex.EncodeToString(h[:])
}

func resolveVMResourcesForAttachment(
	ctx context.Context,
	baseKubeconfig *rest.Config,
	testClusterRes *cluster.TestClusterResources,
	preferredNamespace string,
) (*cluster.VMResources, error) {
	if testClusterRes != nil && testClusterRes.VMResources != nil && len(testClusterRes.VMResources.VMNames) > 0 {
		ns := testClusterRes.VMResources.Namespace
		if ns == "" {
			ns = preferredNamespace
		}
		if ns == "" {
			return nil, fmt.Errorf("VMResources has VM names, but namespace is empty")
		}
		return &cluster.VMResources{
			Namespace:   ns,
			VMNames:     append([]string(nil), testClusterRes.VMResources.VMNames...),
			SetupVMName: testClusterRes.VMResources.SetupVMName,
		}, nil
	}

	seen := map[string]struct{}{}
	namespaces := make([]string, 0, 3)
	addNS := func(ns string) {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			return
		}
		if _, ok := seen[ns]; ok {
			return
		}
		seen[ns] = struct{}{}
		namespaces = append(namespaces, ns)
	}

	if testClusterRes != nil && testClusterRes.VMResources != nil {
		addNS(testClusterRes.VMResources.Namespace)
	}
	addNS(os.Getenv("TEST_CLUSTER_NAMESPACE"))
	addNS(preferredNamespace)

	var tried []string
	for _, ns := range namespaces {
		tried = append(tried, ns)
		vmNames, err := e2ekubernetes.ListVirtualMachineNames(ctx, baseKubeconfig, ns)
		if err != nil {
			slog.Warn(fmt.Sprintf("Failed to list VirtualMachines in namespace %s: %v", ns, err))
			continue
		}
		if len(vmNames) == 0 {
			slog.Info(fmt.Sprintf("No VirtualMachines found in namespace %s", ns))
			continue
		}

		slog.Info(fmt.Sprintf("Discovered %d VirtualMachines in namespace %s: %v", len(vmNames), ns, vmNames))
		return &cluster.VMResources{
			Namespace: ns,
			VMNames:   vmNames,
		}, nil
	}

	return nil, fmt.Errorf("no VirtualMachines found in base cluster (checked namespaces: %v)", tried)
}
