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
	"errors"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sncv1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// waitForReadyNodes waits for the expected number of nodes to be in Ready state.
// It logs progress and waits up to the specified timeout.
func waitForReadyNodes(ctx context.Context, c client.Client, expectedCount int, timeout time.Duration) error {
	slog.Debug(fmt.Sprintf("CHECK: Waiting for %d Ready nodes (timeout: %v)", expectedCount, timeout))

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var nodeList corev1.NodeList
		if err := c.List(ctx, &nodeList); err != nil {
			slog.Warn(fmt.Sprintf("Failed to list nodes: %v", err))
			time.Sleep(pollingInterval)
			continue
		}

		readyCount := 0
		for _, node := range nodeList.Items {
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					readyCount++
					break
				}
			}
		}

		if readyCount >= expectedCount {
			slog.Debug(fmt.Sprintf("PASS: Found %d Ready nodes", readyCount))
			return nil
		}

		slog.Info(fmt.Sprintf("Found %d/%d Ready nodes, waiting...", readyCount, expectedCount))
		time.Sleep(pollingInterval)
	}

	return fmt.Errorf("timeout waiting for %d Ready nodes after %v", expectedCount, timeout)
}

// verifySNCModuleRunning checks that the sds-node-configurator module is enabled
// and all pods of its daemonset are running.
func verifySNCModuleRunning(ctx context.Context, c client.Client, timeout time.Duration) error {
	slog.Debug(fmt.Sprintf("CHECK: Verifying %s module is running (timeout: %v)", moduleNameSNC, timeout))

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if daemonset exists and has desired number of pods ready
		var ds appsv1.DaemonSet
		err := c.Get(ctx, client.ObjectKey{
			Namespace: "d8-" + moduleNameSNC,
			Name:      moduleNameSNC,
		}, &ds)
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info(fmt.Sprintf("DaemonSet %s not found yet, waiting...", moduleNameSNC))
				time.Sleep(pollingInterval)
				continue
			}
			return fmt.Errorf("failed to get daemonset %s: %w", moduleNameSNC, err)
		}

		if ds.Status.NumberReady == ds.Status.DesiredNumberScheduled && ds.Status.DesiredNumberScheduled > 0 {
			slog.Debug(fmt.Sprintf("PASS: DaemonSet %s has %d/%d pods ready", moduleNameSNC, ds.Status.NumberReady, ds.Status.DesiredNumberScheduled))
			return nil
		}

		slog.Info(fmt.Sprintf("DaemonSet %s has %d/%d pods ready, waiting...", moduleNameSNC, ds.Status.NumberReady, ds.Status.DesiredNumberScheduled))
		time.Sleep(pollingInterval)
	}

	return fmt.Errorf("timeout waiting for %s module to be running after %v", moduleNameSNC, timeout)
}

// verifySRVModuleOldControlPlane checks that sds-replicated-volume is enabled in old control plane mode.
// It verifies that linstor-controller deployment and linstor-node daemonset exist and are running.
func verifySRVModuleOldControlPlane(ctx context.Context, c client.Client, timeout time.Duration) error {
	slog.Debug(fmt.Sprintf("CHECK: Verifying %s module in old control plane mode (timeout: %v)", moduleNameSRV, timeout))

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check linstor-controller deployment
		var deploy appsv1.Deployment
		err := c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      linstorControllerDeployment,
		}, &deploy)
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info(fmt.Sprintf("Deployment %s not found yet, waiting...", linstorControllerDeployment))
				time.Sleep(pollingInterval)
				continue
			}
			return fmt.Errorf("failed to get deployment %s: %w", linstorControllerDeployment, err)
		}

		if deploy.Status.ReadyReplicas == 0 {
			slog.Info(fmt.Sprintf("Deployment %s has 0 ready replicas, waiting...", linstorControllerDeployment))
			time.Sleep(pollingInterval)
			continue
		}

		// Check linstor-node daemonset
		var ds appsv1.DaemonSet
		err = c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      linstorNodeDaemonSet,
		}, &ds)
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info(fmt.Sprintf("DaemonSet %s not found yet, waiting...", linstorNodeDaemonSet))
				time.Sleep(pollingInterval)
				continue
			}
			return fmt.Errorf("failed to get daemonset %s: %w", linstorNodeDaemonSet, err)
		}

		if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled || ds.Status.DesiredNumberScheduled == 0 {
			slog.Info(fmt.Sprintf("DaemonSet %s has %d/%d pods ready, waiting...", linstorNodeDaemonSet, ds.Status.NumberReady, ds.Status.DesiredNumberScheduled))
			time.Sleep(pollingInterval)
			continue
		}

		slog.Debug(fmt.Sprintf("PASS: %s old control plane is running (controller: %d replicas, node: %d/%d pods)", moduleNameSRV, deploy.Status.ReadyReplicas, ds.Status.NumberReady, ds.Status.DesiredNumberScheduled))
		return nil
	}

	return fmt.Errorf("timeout waiting for %s old control plane to be running after %v", moduleNameSRV, timeout)
}

// verifyNoResourcesExist checks that no test-related resources exist in the cluster.
// It checks for LVMVolumeGroups, ReplicatedStoragePools, ReplicatedStorageClasses,
// ReplicatedVolumes, ReplicatedVolumeReplicas, DRBDResources, ReplicatedVolumeAttachments,
// and LVMLogicalVolumes.
// If CRDs are not registered (e.g., in old control plane mode), this is considered as no resources existing.
func verifyNoResourcesExist(ctx context.Context, c client.Client) error {
	slog.Debug("CHECK: Verifying no existing resources in cluster")

	// Helper to check list result, treating CRD-not-found as success (no resources)
	checkList := func(list client.ObjectList, resourceName string) (bool, error) {
		if err := c.List(ctx, list); err != nil {
			// Check if error is due to CRD not being registered (NoKindMatchError)
			// This occurs when the API group/version is not registered in the cluster
			var noKindMatchErr *meta.NoKindMatchError
			if errors.As(err, &noKindMatchErr) {
				slog.Info(fmt.Sprintf("%s API not available (CRD not registered), treating as no resources", resourceName))
				return true, nil
			}
			return false, fmt.Errorf("failed to list %s: %w", resourceName, err)
		}
		return false, nil
	}

	// Check LVMVolumeGroups
	var lvgList sncv1.LVMVolumeGroupList
	skip, err := checkList(&lvgList, "LVMVolumeGroups")
	if err != nil {
		return err
	}
	if !skip && len(lvgList.Items) > 0 {
		return fmt.Errorf("found %d existing LVMVolumeGroups, expected 0", len(lvgList.Items))
	}

	// Check ReplicatedStoragePools
	var rspList srvv1.ReplicatedStoragePoolList
	skip, err = checkList(&rspList, "ReplicatedStoragePools")
	if err != nil {
		return err
	}
	if !skip && len(rspList.Items) > 0 {
		return fmt.Errorf("found %d existing ReplicatedStoragePools, expected 0", len(rspList.Items))
	}

	// Check ReplicatedStorageClasses
	var rscList srvv1.ReplicatedStorageClassList
	skip, err = checkList(&rscList, "ReplicatedStorageClasses")
	if err != nil {
		return err
	}
	if !skip && len(rscList.Items) > 0 {
		return fmt.Errorf("found %d existing ReplicatedStorageClasses, expected 0", len(rscList.Items))
	}

	// Check ReplicatedVolumes
	var rvList srvv1.ReplicatedVolumeList
	skip, err = checkList(&rvList, "ReplicatedVolumes")
	if err != nil {
		return err
	}
	if !skip && len(rvList.Items) > 0 {
		return fmt.Errorf("found %d existing ReplicatedVolumes, expected 0", len(rvList.Items))
	}

	// Check ReplicatedVolumeReplicas
	var rvrList srvv1.ReplicatedVolumeReplicaList
	skip, err = checkList(&rvrList, "ReplicatedVolumeReplicas")
	if err != nil {
		return err
	}
	if !skip && len(rvrList.Items) > 0 {
		return fmt.Errorf("found %d existing ReplicatedVolumeReplicas, expected 0", len(rvrList.Items))
	}

	// Check DRBDResources
	var drbdrList srvv1.DRBDResourceList
	skip, err = checkList(&drbdrList, "DRBDResources")
	if err != nil {
		return err
	}
	if !skip && len(drbdrList.Items) > 0 {
		return fmt.Errorf("found %d existing DRBDResources, expected 0", len(drbdrList.Items))
	}

	// Check ReplicatedVolumeAttachments
	var rvaList srvv1.ReplicatedVolumeAttachmentList
	skip, err = checkList(&rvaList, "ReplicatedVolumeAttachments")
	if err != nil {
		return err
	}
	if !skip && len(rvaList.Items) > 0 {
		return fmt.Errorf("found %d existing ReplicatedVolumeAttachments, expected 0", len(rvaList.Items))
	}

	// Check LVMLogicalVolumes
	var llvList sncv1.LVMLogicalVolumeList
	skip, err = checkList(&llvList, "LVMLogicalVolumes")
	if err != nil {
		return err
	}
	if !skip && len(llvList.Items) > 0 {
		return fmt.Errorf("found %d existing LVMLogicalVolumes, expected 0", len(llvList.Items))
	}

	slog.Debug("PASS: No existing resources found")
	return nil
}

// verifyLinstorClean checks that Linstor is in a clean state:
// - All nodes are online
// - Only DfltDisklessStorPool exists on each node
// - No resources exist
func verifyLinstorClean(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config) error {
	slog.Debug("CHECK: Verifying Linstor is in clean state")

	// Get linstor state using JSON output
	state, err := saveLinstorState(ctx, clientset, restConfig, "verify-clean")
	if err != nil {
		return fmt.Errorf("failed to get linstor state: %w", err)
	}
	if len(state.Nodes) == 0 {
		return fmt.Errorf("linstor node list is empty, expected at least one node")
	}
	if len(state.StoragePools) == 0 {
		return fmt.Errorf("linstor storage pool list is empty, expected one diskless pool per node")
	}

	// Check that all nodes are ONLINE
	for _, node := range state.Nodes {
		if node.ConnectionStatus != "ONLINE" {
			return fmt.Errorf("linstor node %s is not ONLINE (status: %s)", node.Name, node.ConnectionStatus)
		}
	}

	// Check storage pools count and expected defaults per node
	if len(state.StoragePools) != len(state.Nodes) {
		return fmt.Errorf("unexpected linstor storage pool count: got %d, expected %d (one pool per node)",
			len(state.StoragePools), len(state.Nodes))
	}

	for _, sp := range state.StoragePools {
		if sp.StoragePool != "DfltDisklessStorPool" {
			return fmt.Errorf("found non-default storage pool %s on node %s, expected only DfltDisklessStorPool", sp.StoragePool, sp.NodeName)
		}
		if sp.Driver != "DISKLESS" {
			return fmt.Errorf("storage pool %s on node %s has provider_kind %s, expected DISKLESS", sp.StoragePool, sp.NodeName, sp.Driver)
		}
	}

	// Check that no resources exist
	if len(state.Resources) > 0 {
		return fmt.Errorf("found %d linstor resources, expected 0", len(state.Resources))
	}

	// Check that no volumes exist
	if len(state.Volumes) > 0 {
		return fmt.Errorf("found %d linstor volumes, expected 0", len(state.Volumes))
	}

	slog.Debug("PASS: Linstor is clean (only DfltDisklessStorPool exists, no resources)")
	return nil
}
