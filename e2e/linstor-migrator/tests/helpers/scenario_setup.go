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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

// MigratorSetupConfig describes what baseline configuration should be created
// before running migration-specific assertions.
type MigratorSetupConfig struct {
	RunID            string
	Namespace        string
	StorageClassName string

	ExpectedRSPCount int
	ExpectedRSCCount int
}

// MigratorSetupResult contains all resources prepared for migration and
// verification stages.
type MigratorSetupResult struct {
	RunID             string
	AttachedDisks     []*attachedDiskResult
	LVGResults        []*lvmVolumeGroupResult
	RSPResults        []*RSPResult
	RSCResults        []*rscResult
	PodResults        []*PodPVCResult
	MigratedResources []string

	OldRSPs       []RSPBaseline
	PodNames      []string
	FileChecksums map[string]string
	LinstorBefore *LinstorState
	StateFilePath string
}

// PrepareOrRestoreMigratorScenarioConfig controls prepare/restore flow.
type PrepareOrRestoreMigratorScenarioConfig struct {
	SetupConfig    MigratorSetupConfig
	PreviousRunID  string
	RequireFocused bool
}

// PrepareMigratorScenario creates the baseline storage configuration used by
// migrator e2e tests and returns all created objects for subsequent checks.
func PrepareMigratorScenario(
	ctx context.Context,
	k8sClient client.Client,
	clientset kubernetes.Interface,
	restConfig *rest.Config,
	testClusterRes *cluster.TestClusterResources,
	cfg MigratorSetupConfig,
) (*MigratorSetupResult, error) {
	cfg = normalizeSetupConfig(cfg)

	result := &MigratorSetupResult{
		RunID:         cfg.RunID,
		FileChecksums: make(map[string]string),
	}

	// ========== INITIAL ENVIRONMENT VALIDATION ==========
	slog.Info("========== Environment Validation ==========")

	slog.Info("STEP: Waiting for Ready nodes", "count", nodeCount)
	if err := waitForReadyNodes(ctx, k8sClient, nodeCount, defaultWaitTimeout); err != nil {
		return nil, fmt.Errorf("failed waiting for Ready nodes: %w", err)
	}

	slog.Info("STEP: Verifying module is running", "module", moduleNameSNC)
	if err := verifySNCModuleRunning(ctx, k8sClient, defaultWaitTimeout); err != nil {
		return nil, fmt.Errorf("SNC module is not running: %w", err)
	}

	slog.Info("STEP: Verifying module in old control plane mode", "module", moduleNameSRV)
	if err := verifySRVModuleOldControlPlane(ctx, k8sClient, defaultWaitTimeout); err != nil {
		return nil, fmt.Errorf("SRV module is not in old control plane mode: %w", err)
	}

	slog.Info("STEP: Verifying no existing resources")
	if err := verifyNoResourcesExist(ctx, k8sClient); err != nil {
		return nil, fmt.Errorf("cluster has existing resources that must be cleaned up: %w", err)
	}

	slog.Info("STEP: Verifying Linstor is clean")
	if err := verifyLinstorClean(ctx, clientset, restConfig); err != nil {
		return nil, fmt.Errorf("linstor is not in clean state: %w", err)
	}

	slog.Info("========== Environment Validation Complete ==========")

	// ========== DISK ATTACHMENT AND LVG CREATION ==========
	slog.Info("========== Disk Attachment and LVG Creation ==========")

	attachConfig := diskAttachmentConfig{
		RunID:            cfg.RunID,
		DiskSize:         diskSize,
		StorageClassName: cfg.StorageClassName,
		Namespace:        cfg.Namespace,
	}

	attachedDisks, err := attachDisksToAllNodes(ctx, testClusterRes.BaseKubeconfig, testClusterRes, k8sClient, attachConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to attach disks to nodes: %w", err)
	}
	if len(attachedDisks) < nodeCount {
		return nil, fmt.Errorf("expected at least %d attached disks, got %d", nodeCount, len(attachedDisks))
	}
	result.AttachedDisks = attachedDisks

	lvgResults, err := createLVMVolumeGroups(ctx, k8sClient, attachedDisks, cfg.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to create LVMVolumeGroups: %w", err)
	}
	if len(lvgResults) != len(attachedDisks) {
		return nil, fmt.Errorf("expected LVG count to match disk count: disks=%d, lvgs=%d", len(attachedDisks), len(lvgResults))
	}
	result.LVGResults = lvgResults

	slog.Info("========== Disk Attachment and LVG Creation Complete ==========")

	// ========== RSP CREATION ==========
	slog.Info("========== RSP Creation ==========")

	rspResults, err := createReplicatedStoragePools(ctx, k8sClient, lvgResults, cfg.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to create ReplicatedStoragePools: %w", err)
	}
	if len(rspResults) != cfg.ExpectedRSPCount {
		return nil, fmt.Errorf("expected %d RSPs, got %d", cfg.ExpectedRSPCount, len(rspResults))
	}
	result.RSPResults = rspResults
	result.OldRSPs = make([]RSPBaseline, len(rspResults))
	for i, rsp := range rspResults {
		result.OldRSPs[i] = RSPBaseline{
			Name: rsp.Name,
			Spec: rsp.RSP.Spec,
		}
	}

	linstorSPs, err := linstorStoragePoolList(ctx, clientset, restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get linstor storage pools: %w", err)
	}

	customPoolCount := 0
	for _, sp := range linstorSPs {
		if sp.StoragePool != "DfltDisklessStorPool" {
			customPoolCount++
			slog.Info("Found Linstor storage pool", "pool", sp.StoragePool, "node", sp.NodeName, "driver", sp.Driver)
		}
	}
	if customPoolCount == 0 {
		return nil, fmt.Errorf("expected custom linstor storage pools to exist")
	}

	slog.Info("========== RSP Creation Complete ==========")

	// ========== RSC/POD CREATION ==========
	slog.Info("========== RSC and Pod Creation ==========")

	rscResults, err := createReplicatedStorageClasses(ctx, k8sClient, result.RSPResults, cfg.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to create ReplicatedStorageClasses: %w", err)
	}
	if len(rscResults) != cfg.ExpectedRSCCount {
		return nil, fmt.Errorf("expected %d RSCs, got %d", cfg.ExpectedRSCCount, len(rscResults))
	}
	result.RSCResults = rscResults

	podResults, createErr := createPodsWithPVCs(ctx, k8sClient, rscResults, cfg.RunID)
	if createErr != nil {
		return nil, fmt.Errorf("failed to create Pod/PVC resources: %w", createErr)
	}
	result.PodResults = podResults
	result.PodNames = make([]string, len(podResults))
	for i, pod := range podResults {
		result.PodNames[i] = pod.PodName
	}

	slog.Info("Writing test files in pods", "count", len(podResults))
	for _, pod := range podResults {
		checksum, writeErr := writeFileWithChecksum(ctx, clientset, restConfig, pod.PodName, TestNamespace)
		if writeErr != nil {
			return nil, fmt.Errorf("failed to write test file in pod %s: %w", pod.PodName, writeErr)
		}
		result.FileChecksums[pod.PodName] = checksum.Checksum
	}

	slog.Info("========== Recording migrated resource names ==========")

	migratedResources := make([]string, 0, len(podResults))
	for _, pod := range podResults {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: pod.PVCName, Namespace: cfg.Namespace}, &pvc); err != nil {
			return nil, fmt.Errorf("failed to get PVC %s: %w", pod.PVCName, err)
		}
		pvName := pvc.Spec.VolumeName
		if pvName == "" {
			return nil, fmt.Errorf("PVC %s has no bound PV (VolumeName is empty)", pod.PVCName)
		}
		migratedResources = append(migratedResources, pvName)
	}

	result.MigratedResources = migratedResources
	slog.Info("========== Migrated resource names recorded ==========", "count", len(migratedResources))

	slog.Info("Saving Linstor state before migration")
	linstorBefore, saveErr := collectLinstorState(ctx, clientset, restConfig, cfg.RunID)
	if saveErr != nil {
		return nil, fmt.Errorf("failed to save linstor state: %w", saveErr)
	}
	result.LinstorBefore = linstorBefore
	logLinstorState(linstorBefore)

	stateFilePath, stateErr := SaveMigratorScenarioState(&MigratorScenarioState{
		RunID:             cfg.RunID,
		OldRSPs:           result.OldRSPs,
		PodNames:          result.PodNames,
		FileChecksums:     result.FileChecksums,
		LinstorBefore:     result.LinstorBefore,
		MigratedResources: result.MigratedResources,
	})
	if stateErr != nil {
		return nil, fmt.Errorf("failed to save migrator scenario state: %w", stateErr)
	}
	result.StateFilePath = stateFilePath
	slog.Info("Migrator scenario state saved", "path", stateFilePath)

	slog.Info("========== RSC and Pod Creation Complete ==========")

	return result, nil
}

// PrepareOrRestoreMigratorScenario either prepares a new scenario or restores
// previously saved post-check state by TEST_PREVIOUS_RUNID.
func PrepareOrRestoreMigratorScenario(
	ctx context.Context,
	k8sClient client.Client,
	clientset kubernetes.Interface,
	restConfig *rest.Config,
	testClusterRes *cluster.TestClusterResources,
	cfg PrepareOrRestoreMigratorScenarioConfig,
) (*MigratorSetupResult, error) {
	cfg.SetupConfig = normalizeSetupConfig(cfg.SetupConfig)
	previousRunID := strings.TrimSpace(cfg.PreviousRunID)
	if previousRunID == "" {
		return PrepareMigratorScenario(ctx, k8sClient, clientset, restConfig, testClusterRes, cfg.SetupConfig)
	}

	restoredState, restoredFilePath, restoreErr := RestoreMigratorScenarioState(previousRunID)
	if restoreErr == nil {
		suiteConfig, _ := GinkgoConfiguration()
		hasFilter := len(suiteConfig.FocusStrings) > 0 || len(suiteConfig.FocusFiles) > 0 || suiteConfig.LabelFilter != ""
		if cfg.RequireFocused && !hasFilter {
			return nil, fmt.Errorf(
				"TEST_PREVIOUS_RUNID is incompatible with full suite run (no focus)",
			)
		}

		slog.Info("Restore mode enabled, loaded state", "path", restoredFilePath, "runID", restoredState.RunID)
		return &MigratorSetupResult{
			RunID:             restoredState.RunID,
			OldRSPs:           append([]RSPBaseline(nil), restoredState.OldRSPs...),
			PodNames:          append([]string(nil), restoredState.PodNames...),
			FileChecksums:     restoredState.FileChecksums,
			LinstorBefore:     restoredState.LinstorBefore,
			StateFilePath:     restoredFilePath,
			MigratedResources: restoredState.MigratedResources,
		}, nil
	}

	if errors.Is(restoreErr, ErrScenarioStateNotFound) {
		slog.Warn("Restore state not found, preparing new scenario", "runID", previousRunID)
		return PrepareMigratorScenario(ctx, k8sClient, clientset, restConfig, testClusterRes, cfg.SetupConfig)
	}

	return nil, fmt.Errorf("failed to restore scenario state for TEST_PREVIOUS_RUNID=%s: %w", previousRunID, restoreErr)
}

func normalizeSetupConfig(cfg MigratorSetupConfig) MigratorSetupConfig {
	if cfg.Namespace == "" {
		cfg.Namespace = TestNamespace
	}
	if cfg.ExpectedRSPCount == 0 {
		cfg.ExpectedRSPCount = 2
	}
	if cfg.ExpectedRSCCount == 0 {
		cfg.ExpectedRSCCount = 6
	}

	return cfg
}

// SimulateLostPVs simulates a scenario where PVCs and PVs are lost
// before migration. It scales linstor-controller to 0, deletes pods,
// removes PVCs (clearing finalizers), and deletes PVs (clearing finalizers).
func SimulateLostPVs(ctx context.Context, c client.Client, podNames []string) error {
	slog.Info("========== Simulating Lost PVs ==========")

	// a. Scale deployment/linstor-controller to 0 replicas.
	slog.Info("Scaling linstor-controller to 0 replicas")
	var controllerDeploy appsv1.Deployment
	key := client.ObjectKey{Namespace: namespaceSRV, Name: linstorControllerDeployment}
	if err := c.Get(ctx, key, &controllerDeploy); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get linstor-controller deployment: %w", err)
		}
		slog.Info("linstor-controller deployment not found, skipping scale-down")
	} else {
		var replicas int32
		patchPayload := []map[string]interface{}{
			{"op": "replace", "path": "/spec/replicas", "value": replicas},
		}
		patchBytes, err := json.Marshal(patchPayload)
		if err != nil {
			return fmt.Errorf("failed to marshal patch for linstor-controller: %w", err)
		}
		if err := c.Patch(ctx, &controllerDeploy, client.RawPatch(types.JSONPatchType, patchBytes)); err != nil {
			return fmt.Errorf("failed to scale linstor-controller to 0: %w", err)
		}

		slog.Info("Waiting for linstor-controller readyReplicas to reach 0")
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			var deploy appsv1.Deployment
			if err := c.Get(ctx, key, &deploy); err != nil {
				if apierrors.IsNotFound(err) {
					break
				}
				return fmt.Errorf("failed to get linstor-controller: %w", err)
			}
			if deploy.Status.ReadyReplicas == 0 {
				slog.Info("linstor-controller scaled to 0 ready replicas")
				break
			}
			time.Sleep(pollingInterval)
		}
	}

	// b. Delete the given pods.
	for _, podName := range podNames {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: TestNamespace,
			},
		}
		if err := c.Delete(ctx, pod); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete pod %s: %w", podName, err)
			}
		}
		slog.Info("Deleted pod", "name", podName)
	}

	// Wait until all pods are fully removed before proceeding to PVC deletion.
	slog.Info("Waiting for all pods to be fully deleted")
	podDeadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(podDeadline) {
		allGone := true
		for _, podName := range podNames {
			var pod corev1.Pod
			err := c.Get(ctx, client.ObjectKey{Name: podName, Namespace: TestNamespace}, &pod)
			if err == nil {
				allGone = false
				break
			}
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check pod %s deletion status: %w", podName, err)
			}
		}
		if allGone {
			slog.Info("All pods are fully deleted")
			break
		}
		time.Sleep(pollingInterval)
	}
	if time.Now().After(podDeadline) {
		return fmt.Errorf("timeout waiting for pods to be fully deleted after 3 minutes")
	}

	// c. List PVCs having the label migrator-e2e. For each: collect PV name,
	//    delete the PVC, then clear finalizers.
	var pvcList corev1.PersistentVolumeClaimList
	if err := c.List(ctx, &pvcList, client.InNamespace(TestNamespace)); err != nil {
		return fmt.Errorf("failed to list PVCs: %w", err)
	}

	pvNames := make(map[string]bool)
	removeFinalizersPatch := []byte(`{"metadata":{"finalizers":null}}`)
	for _, pvc := range pvcList.Items {
		if _, ok := pvc.Labels["migrator-e2e"]; !ok {
			continue
		}

		if pvc.Spec.VolumeName != "" {
			pvNames[pvc.Spec.VolumeName] = true
		}

		// Delete first, then clear finalizers so the API server removes the
		// finalizer entry from the deleted object immediately.
		slog.Info("Deleting PVC and clearing finalizers", "pvc", pvc.Name)
		_ = c.Delete(ctx, &pvc)
		_ = c.Patch(ctx, &pvc, client.RawPatch(types.MergePatchType, removeFinalizersPatch))

		slog.Info("Deleted PVC", "name", pvc.Name, "pv", pvc.Spec.VolumeName)
	}

	// d. Delete each bound PV. Delete first then clear finalizers.
	removePVFinalizersPatch := []byte(`{"metadata":{"finalizers":null}}`)
	for pvName := range pvNames {
		var pv corev1.PersistentVolume
		if err := c.Get(ctx, client.ObjectKey{Name: pvName}, &pv); err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info("PV already deleted", "name", pvName)
				continue
			}
			return fmt.Errorf("failed to get PV %s: %w", pvName, err)
		}

		// Delete first, then clear finalizers.
		slog.Info("Deleting PV and clearing finalizers", "pv", pvName)
		_ = c.Delete(ctx, &pv)
		_ = c.Patch(ctx, &pv, client.RawPatch(types.MergePatchType, removePVFinalizersPatch))

		slog.Info("Deleted PV", "name", pvName)
	}

	slog.Info("========== Lost PVs Simulation Complete ==========")
	return nil
}
