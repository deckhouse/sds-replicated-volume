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
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sncv1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// EnableNewControlPlane enables the new control plane in sds-replicated-volume ModuleConfig.
func EnableNewControlPlane(ctx context.Context, c client.Client) error {
	slog.Debug("CHECK: Enabling new control plane in ModuleConfig")

	mc := &unstructured.Unstructured{}
	mc.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ModuleConfig",
	})

	// Get the ModuleConfig first
	if err := c.Get(ctx, client.ObjectKey{Name: moduleNameSRV}, mc); err != nil {
		return fmt.Errorf("failed to get ModuleConfig %s: %w", moduleNameSRV, err)
	}

	// Apply JSON patch to enable newControlPlane.
	// If spec.version/settings are absent, add them first to satisfy webhook validation.
	specVersion, hasSpecVersion, err := unstructured.NestedInt64(mc.Object, "spec", "version")
	if err != nil {
		return fmt.Errorf("failed to inspect ModuleConfig %s for spec.version: %w", moduleNameSRV, err)
	}
	if !hasSpecVersion {
		// Fallback to status.version when spec.version is absent.
		if statusVersion, found, statusErr := unstructured.NestedString(mc.Object, "status", "version"); statusErr == nil && found && statusVersion != "" {
			if parsed, convErr := strconv.ParseInt(statusVersion, 10, 64); convErr == nil {
				specVersion = parsed
			}
		}
		if specVersion == 0 {
			// Conservative default for module config schema version.
			specVersion = 2
		}
	}

	_, hasSettings, err := unstructured.NestedMap(mc.Object, "spec", "settings")
	if err != nil {
		return fmt.Errorf("failed to inspect ModuleConfig %s for spec.settings: %w", moduleNameSRV, err)
	}

	patchPayload := make([]map[string]interface{}, 0, 2)
	if !hasSpecVersion {
		patchPayload = append(patchPayload, map[string]interface{}{
			"op":    "add",
			"path":  "/spec/version",
			"value": specVersion,
		})
	}

	if !hasSettings {
		patchPayload = append(patchPayload, map[string]interface{}{
			"op":   "add",
			"path": "/spec/settings",
			"value": map[string]interface{}{
				"newControlPlane": true,
			},
		})
	} else {
		_, hasNewControlPlane, err := unstructured.NestedFieldNoCopy(mc.Object, "spec", "settings", "newControlPlane")
		if err != nil {
			return fmt.Errorf("failed to inspect ModuleConfig %s for spec.settings.newControlPlane: %w", moduleNameSRV, err)
		}

		patchOp := "add"
		if hasNewControlPlane {
			patchOp = "replace"
		}
		patchPayload = append(patchPayload, map[string]interface{}{
			"op":    patchOp,
			"path":  "/spec/settings/newControlPlane",
			"value": true,
		})
	}
	patch, err := json.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal patch for ModuleConfig %s: %w", moduleNameSRV, err)
	}
	const maxPatchAttempts = 50
	const patchRetryInterval = 10 * time.Second

	var lastPatchErr error
	for attempt := 1; attempt <= maxPatchAttempts; attempt++ {
		err := c.Patch(ctx, mc, client.RawPatch(types.JSONPatchType, patch))
		if err == nil {
			slog.Info(fmt.Sprintf("ModuleConfig %s patch applied on attempt %d/%d", moduleNameSRV, attempt, maxPatchAttempts))
			lastPatchErr = nil
			break
		}

		lastPatchErr = err
		if attempt == maxPatchAttempts {
			break
		}

		slog.Warn(fmt.Sprintf("Failed to patch ModuleConfig %s (attempt %d/%d): %v. Retrying in %v...", moduleNameSRV, attempt, maxPatchAttempts, err, patchRetryInterval))

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while patching ModuleConfig %s after %d/%d attempts: %w",
				moduleNameSRV, attempt, maxPatchAttempts, ctx.Err())
		case <-time.After(patchRetryInterval):
		}
	}

	if lastPatchErr != nil {
		return fmt.Errorf("failed to patch ModuleConfig %s after %d attempts: %w", moduleNameSRV, maxPatchAttempts, lastPatchErr)
	}

	slog.Debug("PASS: New control plane enabled")
	return nil
}

// WaitForControlPlaneComponentsMigrated waits for control-plane component switch:
// - New controller deployment appears and is ready
// - New agent daemonset appears and is ready
// - Old linstor-controller deployment is removed
// - Old linstor-node daemonset is removed
func WaitForControlPlaneComponentsMigrated(ctx context.Context, c client.Client, timeout time.Duration) error {
	slog.Debug(fmt.Sprintf("CHECK: Waiting for control-plane components to switch (timeout: %v)", timeout))

	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		// Check if new controller deployment exists and is ready
		var newController appsv1.Deployment
		err = c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      newControllerDeployment,
		}, &newController)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get new controller deployment: %w", err)
			}
			slog.Info("New controller deployment not yet created, waiting...")
			time.Sleep(pollingInterval)
			continue
		}

		if newController.Status.ReadyReplicas == 0 {
			slog.Info("New controller has 0 ready replicas, waiting...")
			time.Sleep(pollingInterval)
			continue
		}

		// Check if new agent daemonset exists and is ready
		var newAgent appsv1.DaemonSet
		err = c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      newAgentDaemonSet,
		}, &newAgent)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get new agent daemonset: %w", err)
			}
			slog.Info("New agent daemonset not yet created, waiting...")
			time.Sleep(pollingInterval)
			continue
		}

		if newAgent.Status.NumberReady != newAgent.Status.DesiredNumberScheduled || newAgent.Status.DesiredNumberScheduled == 0 {
			slog.Info(fmt.Sprintf("New agent has %d/%d ready pods, waiting...", newAgent.Status.NumberReady, newAgent.Status.DesiredNumberScheduled))
			time.Sleep(pollingInterval)
			continue
		}

		// Check if old linstor-controller deployment is removed
		var oldController appsv1.Deployment
		err = c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      linstorControllerDeployment,
		}, &oldController)
		if err == nil {
			slog.Info("Old linstor-controller still exists, waiting...")
			time.Sleep(pollingInterval)
			continue
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check old controller deployment: %w", err)
		}

		// Check if old linstor-node daemonset is removed
		var oldNode appsv1.DaemonSet
		err = c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      linstorNodeDaemonSet,
		}, &oldNode)
		if err == nil {
			slog.Info("Old linstor-node daemonset still exists, waiting...")
			time.Sleep(pollingInterval)
			continue
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check old node daemonset: %w", err)
		}

		// All conditions met.
		slog.Debug("PASS: Control-plane components switched: new is ready, old is removed")
		slog.Info(fmt.Sprintf("New controller: %d replicas, New agent: %d/%d pods", newController.Status.ReadyReplicas, newAgent.Status.NumberReady, newAgent.Status.DesiredNumberScheduled))
		return nil
	}

	return fmt.Errorf("timeout waiting for control-plane component switch after %v", timeout)
}

// WaitForMigrationComplete waits for migration artifacts to be finalized:
// - ConfigMap control-plane-migration has data.state=all_completed
func WaitForMigrationComplete(ctx context.Context, c client.Client, timeout time.Duration) error {
	slog.Debug(fmt.Sprintf("CHECK: Waiting for migration artifacts completion (timeout: %v)", timeout))

	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		// Check migration status configmap.
		var migrationCM corev1.ConfigMap
		err = c.Get(ctx, client.ObjectKey{
			Namespace: namespaceSRV,
			Name:      "control-plane-migration",
		}, &migrationCM)
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info("ConfigMap control-plane-migration not found yet, waiting...")
				time.Sleep(pollingInterval)
				continue
			}
			return fmt.Errorf("failed to get ConfigMap control-plane-migration: %w", err)
		}

		migrationState := migrationCM.Data["state"]
		if migrationState != "all_completed" {
			slog.Info(fmt.Sprintf("ConfigMap control-plane-migration state is %q, waiting for all_completed...", migrationState))
			time.Sleep(pollingInterval)
			continue
		}

		slog.Debug("PASS: Migration artifacts completed (state=all_completed)")
		return nil
	}

	return fmt.Errorf("timeout waiting for migration artifacts completion after %v", timeout)
}

// VerifyMigratorArtifactsOnMaster verifies that migrator artifacts exist on one
// of the master/control-plane nodes and that required files/directories are
// present with non-zero file sizes (including the linstor-viewer backup helper).
// It also runs linstor-viewer against crs.gz for node list, storage-pool list,
// and volume list; each command must exit 0 and print non-whitespace output.
func VerifyMigratorArtifactsOnMaster(ctx context.Context, c client.Client, clientset kubernetes.Interface, restConfig *rest.Config) []error {
	var nodeList corev1.NodeList
	if err := c.List(ctx, &nodeList); err != nil {
		return []error{fmt.Errorf("failed to list cluster nodes: %w", err)}
	}

	var masterNodes []string
	for i := range nodeList.Items {
		labels := nodeList.Items[i].Labels
		_, hasMasterLabel := labels["node-role.kubernetes.io/master"]
		_, hasControlPlaneLabel := labels["node-role.kubernetes.io/control-plane"]
		if hasMasterLabel || hasControlPlaneLabel {
			masterNodes = append(masterNodes, nodeList.Items[i].Name)
		}
	}
	if len(masterNodes) == 0 {
		return []error{fmt.Errorf("master/control-plane nodes not found")}
	}
	sort.Strings(masterNodes)

	findScript := `
BASE="/opt/deckhouse/tmp/linstor-migrator"
if [ -d "${BASE}" ]; then
  echo "__MIGRATOR_DIR_FOUND__"
fi
`

	checkScript := `
set -e
BASE="/opt/deckhouse/tmp/linstor-migrator"
test -d "${BASE}"
test -d "${BASE}/linstor-backup-db"
test -f "${BASE}/linstor-backup-db/crds.gz"
test -f "${BASE}/linstor-backup-db/crs.gz"
test -f "${BASE}/linstor-backup-db/linstor-viewer"
test -f "${BASE}/linstor-backup-db/readme.txt"
test -f "${BASE}/linstor-migrator.log"
test -s "${BASE}/linstor-backup-db/crds.gz"
test -s "${BASE}/linstor-backup-db/crs.gz"
test -s "${BASE}/linstor-backup-db/linstor-viewer"
test -s "${BASE}/linstor-backup-db/readme.txt"
test -s "${BASE}/linstor-migrator.log"
test -x "${BASE}/linstor-backup-db/linstor-viewer"
`

	viewerScript := `
set -e
BASE="/opt/deckhouse/tmp/linstor-migrator"
V="${BASE}/linstor-backup-db/linstor-viewer"
C="${BASE}/linstor-backup-db/crs.gz"
for sub in "node list" "storage-pool list" "volume list"; do
  set -- ${sub}
  out=$("$V" "$C" "$@") || {
    echo "linstor-viewer ${sub} failed" >&2
    exit 1
  }
  trimmed=$(printf '%s' "$out" | tr -d '[:space:]')
  if [ -z "${trimmed}" ]; then
    echo "linstor-viewer ${sub} produced empty output" >&2
    exit 1
  fi
done
`

	for _, nodeName := range masterNodes {
		agentPods, err := clientset.CoreV1().Pods("d8-sds-node-configurator").List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
		})
		if err != nil {
			slog.Info(fmt.Sprintf("Failed to list sds-node-configurator pods on node %s: %v", nodeName, err))
			continue
		}

		agentPodName := ""
		for _, p := range agentPods.Items {
			if p.Status.Phase != corev1.PodRunning {
				continue
			}
			if strings.HasPrefix(p.Name, "sds-node-configurator") {
				agentPodName = p.Name
				break
			}
		}
		if agentPodName == "" {
			slog.Info(fmt.Sprintf("Running sds-node-configurator pod not found on node %s, skipping", nodeName))
			continue
		}

		findCmd := []string{
			"/opt/deckhouse/sds/bin/nsenter.static", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--",
			"sh", "-ec", findScript,
		}
		out, err := execInPodWithOutputInContainer(
			ctx,
			clientset,
			restConfig,
			agentPodName,
			"d8-sds-node-configurator",
			"sds-node-configurator-agent",
			findCmd,
		)
		if err != nil {
			slog.Info(fmt.Sprintf("Failed to check migrator directory on node %s via pod %s: %v", nodeName, agentPodName, err))
			continue
		}
		if !strings.Contains(out, "__MIGRATOR_DIR_FOUND__") {
			slog.Info(fmt.Sprintf("Migrator directory not found on master node %s, checking next master", nodeName))
			continue
		}

		checkCmd := []string{
			"/opt/deckhouse/sds/bin/nsenter.static", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--",
			"sh", "-ec", checkScript,
		}
		if _, err := execInPodWithOutputInContainer(
			ctx,
			clientset,
			restConfig,
			agentPodName,
			"d8-sds-node-configurator",
			"sds-node-configurator-agent",
			checkCmd,
		); err != nil {
			return []error{fmt.Errorf(
				"migrator artifacts structure validation failed on master node %s via pod %s: %w",
				nodeName, agentPodName, err,
			)}
		}

		viewerCmd := []string{
			"/opt/deckhouse/sds/bin/nsenter.static", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--",
			"sh", "-ec", viewerScript,
		}
		if _, err := execInPodWithOutputInContainer(
			ctx,
			clientset,
			restConfig,
			agentPodName,
			"d8-sds-node-configurator",
			"sds-node-configurator-agent",
			viewerCmd,
		); err != nil {
			return []error{fmt.Errorf(
				"linstor-viewer smoke check failed on master node %s via pod %s: %w",
				nodeName, agentPodName, err,
			)}
		}

		slog.Debug(fmt.Sprintf("PASS: Migrator artifacts and linstor-viewer listings verified on master node %s", nodeName))
		return nil
	}

	return []error{fmt.Errorf(
		"migrator artifacts directory /opt/deckhouse/tmp/linstor-migrator was not found on any master/control-plane node (%d checked)",
		len(masterNodes),
	)}
}

// CompareRSPs compares the RSPs before and after migration.
// It checks that old RSPs are removed and new RSPs are created by the migrator.
func CompareRSPs(ctx context.Context, c client.Client, oldRSPs []RSPBaseline) []error {
	slog.Debug("CHECK: Comparing RSPs before and after migration")

	var errors []error

	// Get current RSPs
	var currentRSPList srvv1.ReplicatedStoragePoolList
	if err := c.List(ctx, &currentRSPList); err != nil {
		return []error{fmt.Errorf("failed to list current RSPs: %w", err)}
	}

	currentRSPNames := make(map[string]bool)
	for _, rsp := range currentRSPList.Items {
		currentRSPNames[rsp.Name] = true
	}

	// Check that old RSPs are removed
	for _, oldRSP := range oldRSPs {
		oldName := oldRSP.Name
		if currentRSPNames[oldName] {
			errors = append(errors, fmt.Errorf("old RSP %s still exists after migration", oldName))
		}
	}

	currentByName := make(map[string]srvv1.ReplicatedStoragePool, len(currentRSPList.Items))
	for _, current := range currentRSPList.Items {
		currentByName[current.Name] = current
	}

	// Check that migrator-created names exist and compare selected spec fields.
	foundMigratedRSPs := 0
	for _, oldRSP := range oldRSPs {
		expectedName := AutoReplicatedStoragePoolNamePrefix + oldRSP.Name
		current, found := currentByName[expectedName]
		if !found {
			errors = append(errors, fmt.Errorf(
				"expected migrated RSP %s was not found (name pattern: %s<old-rsp-name>)",
				expectedName, AutoReplicatedStoragePoolNamePrefix,
			))
			continue
		}
		foundMigratedRSPs++

		if current.Spec.Type != oldRSP.Spec.Type {
			errors = append(errors, fmt.Errorf(
				"RSP %s has type %s, expected %s",
				expectedName, current.Spec.Type, oldRSP.Spec.Type,
			))
		}

		oldLVGs := normalizeRSPBaselineLVMGroups(oldRSP.Spec.LVMVolumeGroups)
		currentLVGs := normalizeRSPBaselineLVMGroups(current.Spec.LVMVolumeGroups)
		if !rspLVMGroupsEqual(oldLVGs, currentLVGs) {
			errors = append(errors, fmt.Errorf(
				"RSP %s lvmVolumeGroups mismatch (old: %+v, new: %+v)",
				expectedName, oldLVGs, currentLVGs,
			))
		}
	}

	slog.Info(fmt.Sprintf("RSP comparison: %d old RSPs removed, %d migrated RSPs found by expected names", len(oldRSPs), foundMigratedRSPs))

	if len(errors) == 0 {
		slog.Debug("PASS: RSP migration verified")
	}

	return errors
}

func listCurrentRSPsByName(ctx context.Context, c client.Client) (map[string]srvv1.ReplicatedStoragePool, error) {
	var currentRSPList srvv1.ReplicatedStoragePoolList
	if err := c.List(ctx, &currentRSPList); err != nil {
		return nil, fmt.Errorf("failed to list current RSPs: %w", err)
	}
	currentByName := make(map[string]srvv1.ReplicatedStoragePool, len(currentRSPList.Items))
	for _, current := range currentRSPList.Items {
		currentByName[current.Name] = current
	}
	return currentByName, nil
}

type RSPSnapshot struct {
	CurrentByName map[string]srvv1.ReplicatedStoragePool
}

func CollectRSPSnapshot(ctx context.Context, c client.Client) (*RSPSnapshot, error) {
	currentByName, err := listCurrentRSPsByName(ctx, c)
	if err != nil {
		return nil, err
	}
	return &RSPSnapshot{CurrentByName: currentByName}, nil
}

func CheckRSPOldNamesRemovedFromSnapshot(snapshot *RSPSnapshot, oldRSPs []RSPBaseline) []error {
	var errors []error
	for _, oldRSP := range oldRSPs {
		if _, exists := snapshot.CurrentByName[oldRSP.Name]; exists {
			errors = append(errors, fmt.Errorf("old RSP %s still exists after migration", oldRSP.Name))
		}
	}
	return errors
}

func CheckRSPMigratedNamesExistFromSnapshot(snapshot *RSPSnapshot, oldRSPs []RSPBaseline) []error {
	var errors []error
	for _, oldRSP := range oldRSPs {
		expectedName := AutoReplicatedStoragePoolNamePrefix + oldRSP.Name
		if _, found := snapshot.CurrentByName[expectedName]; !found {
			errors = append(errors, fmt.Errorf(
				"expected migrated RSP %s was not found (name pattern: %s<old-rsp-name>)",
				expectedName, AutoReplicatedStoragePoolNamePrefix,
			))
		}
	}
	return errors
}

func CheckRSPTypesMatchOldBaselinesFromSnapshot(snapshot *RSPSnapshot, oldRSPs []RSPBaseline) []error {
	var errors []error
	for _, oldRSP := range oldRSPs {
		expectedName := AutoReplicatedStoragePoolNamePrefix + oldRSP.Name
		current, found := snapshot.CurrentByName[expectedName]
		if !found {
			continue
		}
		if current.Spec.Type != oldRSP.Spec.Type {
			errors = append(errors, fmt.Errorf(
				"RSP %s has type %s, expected %s",
				expectedName, current.Spec.Type, oldRSP.Spec.Type,
			))
		}
	}
	return errors
}

func CheckRSPLVMGroupsMatchOldBaselinesFromSnapshot(snapshot *RSPSnapshot, oldRSPs []RSPBaseline) []error {
	var errors []error
	for _, oldRSP := range oldRSPs {
		expectedName := AutoReplicatedStoragePoolNamePrefix + oldRSP.Name
		current, found := snapshot.CurrentByName[expectedName]
		if !found {
			continue
		}
		oldLVGs := normalizeRSPBaselineLVMGroups(oldRSP.Spec.LVMVolumeGroups)
		currentLVGs := normalizeRSPBaselineLVMGroups(current.Spec.LVMVolumeGroups)
		if !rspLVMGroupsEqual(oldLVGs, currentLVGs) {
			errors = append(errors, fmt.Errorf(
				"RSP %s lvmVolumeGroups mismatch (old: %+v, new: %+v)",
				expectedName, oldLVGs, currentLVGs,
			))
		}
	}
	return errors
}

func normalizeRSPBaselineLVMGroups(groups []srvv1.ReplicatedStoragePoolLVMVolumeGroups) []srvv1.ReplicatedStoragePoolLVMVolumeGroups {
	normalized := append([]srvv1.ReplicatedStoragePoolLVMVolumeGroups(nil), groups...)
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Name == normalized[j].Name {
			return normalized[i].ThinPoolName < normalized[j].ThinPoolName
		}
		return normalized[i].Name < normalized[j].Name
	})
	return normalized
}

func rspLVMGroupsEqual(a, b []srvv1.ReplicatedStoragePoolLVMVolumeGroups) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].ThinPoolName != b[i].ThinPoolName {
			return false
		}
	}
	return true
}

// CompareReplicatedVolumeAttachmentsWithLinstor compares RVA resources with Linstor state.
// It checks:
// 1) RVA count equals number of InUse Linstor volumes.
// 2) Each InUse volume has RVA with matching spec.replicatedVolumeName/spec.nodeName.
func CompareReplicatedVolumeAttachmentsWithLinstor(ctx context.Context, c client.Client, linstorState *LinstorState) []error {
	slog.Debug("CHECK: Comparing RVA resources with Linstor state")
	var errors []error

	expectedPairs := make(map[string]bool)
	for _, vol := range linstorState.Volumes {
		if !vol.InUse {
			continue
		}
		key := normalizeAttachmentPairKey(vol.Resource, vol.Node)
		expectedPairs[key] = true
	}
	if len(expectedPairs) == 0 {
		Skip("Skipping RVA comparison: no InUse=true volumes in saved linstor state")
		return nil
	}

	var rvaList srvv1.ReplicatedVolumeAttachmentList
	if err := c.List(ctx, &rvaList); err != nil {
		return []error{fmt.Errorf("failed to list RVAs: %w", err)}
	}

	if len(rvaList.Items) != len(expectedPairs) {
		errors = append(errors, fmt.Errorf(
			"RVA count mismatch: got %d, expected %d (number of InUse Linstor volumes)",
			len(rvaList.Items), len(expectedPairs),
		))
	}

	currentPairs := make(map[string]bool, len(rvaList.Items))
	for _, rva := range rvaList.Items {
		key := normalizeAttachmentPairKey(rva.Spec.ReplicatedVolumeName, rva.Spec.NodeName)
		currentPairs[key] = true

		expectedRVAName := srvv1.FormatReplicatedVolumeAttachmentName(
			rva.Spec.ReplicatedVolumeName, strings.ToLower(rva.Spec.NodeName))
		if rva.Name != expectedRVAName {
			errors = append(errors, fmt.Errorf(
				"RVA name %s does not match expected pattern csi-<replicatedVolumeName>-<lower(nodeName)> (expected %s)",
				rva.Name, expectedRVAName,
			))
		}
	}

	for key := range expectedPairs {
		if !currentPairs[key] {
			errors = append(errors, fmt.Errorf(
				"RVA not found for InUse volume pair %s (expected by linstorState.volumes InUse=true)", key,
			))
		}
	}

	slog.Info(fmt.Sprintf("RVA comparison: %d RVAs vs %d InUse Linstor volumes", len(rvaList.Items), len(expectedPairs)))
	if len(errors) == 0 {
		slog.Debug("PASS: RVA resources match InUse Linstor volumes")
	}
	return errors
}

func normalizeAttachmentPairKey(volumeName, nodeName string) string {
	return fmt.Sprintf("%s/%s", strings.ToLower(volumeName), strings.ToLower(nodeName))
}

func expectedInUseAttachmentPairs(linstorState *LinstorState) map[string]bool {
	expectedPairs := make(map[string]bool)
	for _, vol := range linstorState.Volumes {
		if !vol.InUse {
			continue
		}
		key := normalizeAttachmentPairKey(vol.Resource, vol.Node)
		expectedPairs[key] = true
	}
	return expectedPairs
}

func listCurrentRVA(ctx context.Context, c client.Client) ([]srvv1.ReplicatedVolumeAttachment, error) {
	var rvaList srvv1.ReplicatedVolumeAttachmentList
	if err := c.List(ctx, &rvaList); err != nil {
		return nil, fmt.Errorf("failed to list RVAs: %w", err)
	}
	return rvaList.Items, nil
}

type RVASnapshot struct {
	ExpectedPairs map[string]bool
	CurrentRVAs   []srvv1.ReplicatedVolumeAttachment
	CurrentPairs  map[string]bool
}

func CollectRVASnapshot(ctx context.Context, c client.Client, linstorState *LinstorState) (*RVASnapshot, error) {
	expectedPairs := expectedInUseAttachmentPairs(linstorState)
	rvas, err := listCurrentRVA(ctx, c)
	if err != nil {
		return nil, err
	}
	currentPairs := make(map[string]bool, len(rvas))
	for _, rva := range rvas {
		key := normalizeAttachmentPairKey(rva.Spec.ReplicatedVolumeName, rva.Spec.NodeName)
		currentPairs[key] = true
	}
	return &RVASnapshot{
		ExpectedPairs: expectedPairs,
		CurrentRVAs:   rvas,
		CurrentPairs:  currentPairs,
	}, nil
}

func CheckRVACountMatchesInUseVolumesFromSnapshot(snapshot *RVASnapshot) []error {
	if len(snapshot.ExpectedPairs) == 0 {
		Skip("Skipping RVA checks: no InUse=true volumes in saved linstor state")
		return nil
	}
	if len(snapshot.CurrentRVAs) != len(snapshot.ExpectedPairs) {
		return []error{fmt.Errorf(
			"RVA count mismatch: got %d, expected %d (number of InUse Linstor volumes)",
			len(snapshot.CurrentRVAs), len(snapshot.ExpectedPairs),
		)}
	}
	return nil
}

func CheckRVAPairsMatchInUseVolumesFromSnapshot(snapshot *RVASnapshot) []error {
	if len(snapshot.ExpectedPairs) == 0 {
		Skip("Skipping RVA checks: no InUse=true volumes in saved linstor state")
		return nil
	}
	var errors []error
	for key := range snapshot.ExpectedPairs {
		if !snapshot.CurrentPairs[key] {
			errors = append(errors, fmt.Errorf(
				"RVA not found for InUse volume pair %s (expected by linstorState.volumes InUse=true)", key,
			))
		}
	}
	return errors
}

// CheckRVACSIControllerFinalizerFromSnapshot verifies each RVA has the CSI controller finalizer.
func CheckRVACSIControllerFinalizerFromSnapshot(snapshot *RVASnapshot) []error {
	if len(snapshot.ExpectedPairs) == 0 {
		Skip("Skipping RVA checks: no InUse=true volumes in saved linstor state")
		return nil
	}
	var errors []error
	for _, rva := range snapshot.CurrentRVAs {
		if !slices.Contains(rva.Finalizers, srvv1.CSIControllerFinalizer) {
			errors = append(errors, fmt.Errorf(
				"RVA %s is missing finalizer %q (finalizers=%v)",
				rva.Name, srvv1.CSIControllerFinalizer, rva.Finalizers,
			))
		}
	}
	return errors
}

func CheckRVANamePatternFromSnapshot(snapshot *RVASnapshot) []error {
	if len(snapshot.ExpectedPairs) == 0 {
		Skip("Skipping RVA checks: no InUse=true volumes in saved linstor state")
		return nil
	}
	var errors []error
	for _, rva := range snapshot.CurrentRVAs {
		expectedRVAName := srvv1.FormatReplicatedVolumeAttachmentName(
			rva.Spec.ReplicatedVolumeName, strings.ToLower(rva.Spec.NodeName))
		if rva.Name != expectedRVAName {
			errors = append(errors, fmt.Errorf(
				"RVA name %s does not match expected pattern csi-<replicatedVolumeName>-<lower(nodeName)> (expected %s)",
				rva.Name, expectedRVAName,
			))
		}
	}
	return errors
}

// CompareReplicatedVolumesWithLinstor compares RV resources with Linstor state.
func CompareReplicatedVolumesWithLinstor(ctx context.Context, c client.Client, linstorState *LinstorState) []error {
	slog.Debug("CHECK: Comparing RV resources with Linstor state")
	var errors []error

	var rvList srvv1.ReplicatedVolumeList
	if err := c.List(ctx, &rvList); err != nil {
		return []error{fmt.Errorf("failed to list RVs: %w", err)}
	}

	uniqueResourceNames := make(map[string]bool)
	for _, linstorRes := range linstorState.Resources {
		if linstorRes.Name == "" {
			continue
		}
		uniqueResourceNames[linstorRes.Name] = true
	}

	if len(rvList.Items) != len(uniqueResourceNames) {
		errors = append(errors, fmt.Errorf(
			"RV count mismatch: got %d, expected %d (unique linstor resource names)",
			len(rvList.Items), len(uniqueResourceNames),
		))
	}

	expectedSizeByResource := make(map[string]int64)
	pvByResource := make(map[string]*corev1.PersistentVolume)
	for resourceName := range uniqueResourceNames {
		// This scenario validates only PV-backed resources.
		// Expected RV size must match PV.spec.capacity.storage.
		var pv corev1.PersistentVolume
		err := c.Get(ctx, client.ObjectKey{Name: resourceName}, &pv)
		if err == nil {
			pvCopy := pv
			pvByResource[resourceName] = &pvCopy
			if qty, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok && !qty.IsZero() {
				expectedSizeByResource[resourceName] = qty.Value()
				continue
			}
			errors = append(errors, fmt.Errorf("PV %s has empty spec.capacity.storage", resourceName))
			continue
		}
		if apierrors.IsNotFound(err) {
			errors = append(errors, fmt.Errorf("PV %s not found: this scenario expects PV-backed RVs only", resourceName))
			continue
		}
		errors = append(errors, fmt.Errorf("failed to get PV %s for checks: %w", resourceName, err))
	}

	rvsByName := make(map[string]srvv1.ReplicatedVolume, len(rvList.Items))
	for _, rv := range rvList.Items {
		rvsByName[rv.Name] = rv
		if !uniqueResourceNames[rv.Name] {
			errors = append(errors, fmt.Errorf("unexpected RV %s exists but is absent in linstor resources", rv.Name))
		}
	}

	for resourceName := range uniqueResourceNames {
		rv, ok := rvsByName[resourceName]
		if !ok {
			errors = append(errors, fmt.Errorf("RV %s not found for linstor resource", resourceName))
			continue
		}

		if rv.Name != resourceName {
			errors = append(errors, fmt.Errorf("RV name mismatch: got %s, expected %s", rv.Name, resourceName))
		}

		if rv.Spec.MaxAttachments == nil || *rv.Spec.MaxAttachments != 2 {
			actual := "<nil>"
			if rv.Spec.MaxAttachments != nil {
				actual = fmt.Sprintf("%d", *rv.Spec.MaxAttachments)
			}
			errors = append(errors, fmt.Errorf("RV %s has spec.maxAttachments=%s, expected 2", resourceName, actual))
		}

		if rv.Spec.ConfigurationMode != srvv1.ReplicatedVolumeConfigurationModeAuto {
			errors = append(errors, fmt.Errorf(
				"RV %s has unsupported spec.configurationMode=%q (this scenario expects Auto)",
				resourceName, rv.Spec.ConfigurationMode,
			))
		}
		pv := pvByResource[resourceName]
		if pv == nil {
			errors = append(errors, fmt.Errorf("PV %s is missing for Auto mode checks", resourceName))
		} else {
			if pv.Spec.StorageClassName == "" {
				errors = append(errors, fmt.Errorf("PV %s has empty spec.storageClassName", resourceName))
			} else if rv.Spec.ReplicatedStorageClassName != pv.Spec.StorageClassName {
				errors = append(errors, fmt.Errorf(
					"RV %s has spec.replicatedStorageClassName=%q, expected PV.spec.storageClassName=%q",
					resourceName, rv.Spec.ReplicatedStorageClassName, pv.Spec.StorageClassName,
				))
			}
		}
		if rv.Spec.ManualConfiguration != nil {
			errors = append(errors, fmt.Errorf("RV %s has non-nil spec.manualConfiguration in Auto scenario", resourceName))
		}

		expectedSizeRaw, hasSize := expectedSizeByResource[resourceName]
		if !hasSize {
			errors = append(errors, fmt.Errorf("RV %s size check failed: expected size was not determined", resourceName))
			continue
		}

		actualSize := rv.Spec.Size.Value()
		if actualSize != expectedSizeRaw {
			errors = append(errors, fmt.Errorf(
				"RV %s has spec.size=%d bytes, expected %d bytes",
				resourceName, actualSize, expectedSizeRaw,
			))
		}
	}

	slog.Info(fmt.Sprintf("RV comparison: %d RVs vs %d unique Linstor resources", len(rvList.Items), len(uniqueResourceNames)))
	if len(errors) == 0 {
		slog.Debug("PASS: RV resources match Linstor state")
	}
	return errors
}

func expectedUniqueLinstorResourceNames(linstorState *LinstorState) map[string]bool {
	uniqueResourceNames := make(map[string]bool)
	for _, linstorRes := range linstorState.Resources {
		if linstorRes.Name == "" {
			continue
		}
		uniqueResourceNames[linstorRes.Name] = true
	}
	return uniqueResourceNames
}

type RVSnapshot struct {
	UniqueResourceNames map[string]bool
	RVsByName           map[string]srvv1.ReplicatedVolume
	PVsByResource       map[string]*corev1.PersistentVolume
	RVCount             int
	LinstorState        *LinstorState
}

func CollectRVSnapshot(ctx context.Context, c client.Client, linstorState *LinstorState) (*RVSnapshot, error) {
	var rvList srvv1.ReplicatedVolumeList
	if err := c.List(ctx, &rvList); err != nil {
		return nil, fmt.Errorf("failed to list RVs: %w", err)
	}
	rvsByName := make(map[string]srvv1.ReplicatedVolume, len(rvList.Items))
	for _, rv := range rvList.Items {
		rvsByName[rv.Name] = rv
	}
	uniqueResourceNames := expectedUniqueLinstorResourceNames(linstorState)
	pvsByResource := make(map[string]*corev1.PersistentVolume, len(uniqueResourceNames))
	for resourceName := range uniqueResourceNames {
		var pv corev1.PersistentVolume
		err := c.Get(ctx, client.ObjectKey{Name: resourceName}, &pv)
		if err == nil {
			pvCopy := pv
			pvsByResource[resourceName] = &pvCopy
			continue
		}
		if apierrors.IsNotFound(err) {
			pvsByResource[resourceName] = nil
			continue
		}
		return nil, fmt.Errorf("failed to get PV %s for checks: %w", resourceName, err)
	}
	return &RVSnapshot{
		UniqueResourceNames: uniqueResourceNames,
		RVsByName:           rvsByName,
		PVsByResource:       pvsByResource,
		RVCount:             len(rvList.Items),
		LinstorState:        linstorState,
	}, nil
}

func CheckRVCountMatchesLinstorResourcesFromSnapshot(snapshot *RVSnapshot) []error {
	if snapshot.RVCount != len(snapshot.UniqueResourceNames) {
		return []error{fmt.Errorf(
			"RV count mismatch: got %d, expected %d (unique linstor resource names)",
			snapshot.RVCount, len(snapshot.UniqueResourceNames),
		)}
	}
	return nil
}

func CheckRVNamesMatchLinstorResourcesFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for rvName := range snapshot.RVsByName {
		if !snapshot.UniqueResourceNames[rvName] {
			errors = append(errors, fmt.Errorf("unexpected RV %s exists but is absent in linstor resources", rvName))
		}
	}
	for resourceName := range snapshot.UniqueResourceNames {
		if _, ok := snapshot.RVsByName[resourceName]; !ok {
			errors = append(errors, fmt.Errorf("RV %s not found for linstor resource", resourceName))
		}
	}
	return errors
}

func CheckRVMaxAttachmentsFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}
		if rv.Spec.MaxAttachments == nil || *rv.Spec.MaxAttachments != 2 {
			actual := "<nil>"
			if rv.Spec.MaxAttachments != nil {
				actual = fmt.Sprintf("%d", *rv.Spec.MaxAttachments)
			}
			errors = append(errors, fmt.Errorf("RV %s has spec.maxAttachments=%s, expected 2", resourceName, actual))
		}
	}
	return errors
}

func CheckRVConfigurationModeFromSnapshot(snapshot *RVSnapshot, autoExpected, manualExpected []string) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}

		switch {
		case stringInSlice(resourceName, autoExpected):
			if rv.Spec.ConfigurationMode != srvv1.ReplicatedVolumeConfigurationModeAuto {
				errors = append(errors, fmt.Errorf(
					"RV %s has spec.configurationMode=%q (expected Auto)",
					resourceName, rv.Spec.ConfigurationMode,
				))
			}
		case stringInSlice(resourceName, manualExpected):
			if rv.Spec.ConfigurationMode != srvv1.ReplicatedVolumeConfigurationModeManual {
				errors = append(errors, fmt.Errorf(
					"RV %s has spec.configurationMode=%q (expected Manual)",
					resourceName, rv.Spec.ConfigurationMode,
				))
			}
		default:
			errors = append(errors, fmt.Errorf(
				"RV %s is not expected in auto or manual resource lists",
				resourceName,
			))
		}
	}
	return errors
}

func CheckRVReplicatedStorageClassMatchesPVFromSnapshot(snapshot *RVSnapshot, autoExpected, manualExpected []string) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}

		switch {
		case stringInSlice(resourceName, autoExpected):
			pv := snapshot.PVsByResource[resourceName]
			if pv == nil {
				errors = append(errors, fmt.Errorf("PV %s not found: this scenario expects PV-backed RVs only", resourceName))
				continue
			}
			if pv.Spec.StorageClassName == "" {
				errors = append(errors, fmt.Errorf("PV %s has empty spec.storageClassName", resourceName))
				continue
			}
			if rv.Spec.ReplicatedStorageClassName != pv.Spec.StorageClassName {
				errors = append(errors, fmt.Errorf(
					"RV %s has spec.replicatedStorageClassName=%q, expected PV.spec.storageClassName=%q",
					resourceName, rv.Spec.ReplicatedStorageClassName, pv.Spec.StorageClassName,
				))
			}
		case stringInSlice(resourceName, manualExpected):
			if rv.Spec.ReplicatedStorageClassName != "" {
				errors = append(errors, fmt.Errorf(
					"RV %s has non-empty spec.replicatedStorageClassName=%q (expected empty for Manual-configured resource)",
					resourceName, rv.Spec.ReplicatedStorageClassName,
				))
			}
		default:
			errors = append(errors, fmt.Errorf(
				"RV %s is not expected in auto or manual resource lists",
				resourceName,
			))
		}
	}
	return errors
}

func CheckRVManualConfigurationFromSnapshot(snapshot *RVSnapshot, autoExpected, manualExpected []string) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}

		switch {
		case stringInSlice(resourceName, autoExpected):
			if rv.Spec.ManualConfiguration != nil {
				errors = append(errors, fmt.Errorf(
					"RV %s has non-nil spec.manualConfiguration (expected nil for Auto-configured resource)",
					resourceName,
				))
			}
		case stringInSlice(resourceName, manualExpected):
			if rv.Spec.ManualConfiguration == nil {
				errors = append(errors, fmt.Errorf(
					"RV %s has nil spec.manualConfiguration (expected non-nil for Manual-configured resource)",
					resourceName,
				))
			}
		default:
			errors = append(errors, fmt.Errorf(
				"RV %s is not expected in auto or manual resource lists",
				resourceName,
			))
		}
	}
	return errors
}

// CheckRVManualConfigurationFieldsFromSnapshot verifies that Manual RVs have
// well-formed spec.manualConfiguration:
//   - replicatedStoragePoolName matches AutoReplicatedStoragePoolNamePrefix + <original pool name>
//   - topology is Ignored
//   - failuresToTolerate <= 1
//   - guaranteedMinimumDataRedundancy <= 1
//   - volumeAccess is PreferablyLocal
func CheckRVManualConfigurationFieldsFromSnapshot(snapshot *RVSnapshot, manualExpected []string) []error {
	var errors []error
	for _, resourceName := range manualExpected {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			errors = append(errors, fmt.Errorf("RV %s not found in snapshot", resourceName))
			continue
		}
		mc := rv.Spec.ManualConfiguration
		if mc == nil {
			errors = append(errors, fmt.Errorf("RV %s has nil spec.manualConfiguration", resourceName))
			continue
		}

		// Resolve the expected pool name from the original Linstor storage pool.
		origPool := ""
		for _, v := range snapshot.LinstorState.Volumes {
			if v.Resource == resourceName && v.StoragePool != "DfltDisklessStorPool" {
				origPool = v.StoragePool
				break
			}
		}
		if origPool == "" {
			errors = append(errors, fmt.Errorf(
				"RV %s: original storage pool not found in LinstorState",
				resourceName,
			))
		} else {
			expectedPoolName := AutoReplicatedStoragePoolNamePrefix + origPool
			if mc.ReplicatedStoragePoolName != expectedPoolName {
				errors = append(errors, fmt.Errorf(
					"RV %s has spec.manualConfiguration.replicatedStoragePoolName=%q (expected %q)",
					resourceName, mc.ReplicatedStoragePoolName, expectedPoolName,
				))
			}
		}

		if mc.Topology != srvv1.TopologyIgnored {
			errors = append(errors, fmt.Errorf(
				"RV %s has spec.manualConfiguration.topology=%q (expected Ignored)",
				resourceName, mc.Topology,
			))
		}
		if mc.FailuresToTolerate > 1 {
			errors = append(errors, fmt.Errorf(
				"RV %s has spec.manualConfiguration.failuresToTolerate=%d (expected <= 1)",
				resourceName, mc.FailuresToTolerate,
			))
		}
		if mc.GuaranteedMinimumDataRedundancy > 1 {
			errors = append(errors, fmt.Errorf(
				"RV %s has spec.manualConfiguration.guaranteedMinimumDataRedundancy=%d (expected <= 1)",
				resourceName, mc.GuaranteedMinimumDataRedundancy,
			))
		}
		if mc.VolumeAccess != srvv1.VolumeAccessPreferablyLocal {
			errors = append(errors, fmt.Errorf(
				"RV %s has spec.manualConfiguration.volumeAccess=%q (expected PreferablyLocal)",
				resourceName, mc.VolumeAccess,
			))
		}
	}
	return errors
}

// CheckRVCSIControllerFinalizerFromSnapshot verifies each RV has the CSI controller finalizer.
func CheckRVCSIControllerFinalizerFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}
		if !slices.Contains(rv.Finalizers, srvv1.CSIControllerFinalizer) {
			errors = append(errors, fmt.Errorf(
				"RV %s is missing finalizer %q (finalizers=%v)",
				resourceName, srvv1.CSIControllerFinalizer, rv.Finalizers,
			))
		}
	}
	return errors
}

// CheckRVAdoptAnnotationsAbsentFromSnapshot verifies stage-2 cleanup removed adopt annotations from each RV.
func CheckRVAdoptAnnotationsAbsentFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}
		if rv.Annotations == nil {
			continue
		}
		if _, exists := rv.Annotations[srvv1.AdoptRVRAnnotationKey]; exists {
			errors = append(errors, fmt.Errorf("RV %s still has annotation %q", resourceName, srvv1.AdoptRVRAnnotationKey))
		}
		if _, exists := rv.Annotations[srvv1.AdoptSharedSecretAnnotationKey]; exists {
			errors = append(errors, fmt.Errorf("RV %s still has annotation %q", resourceName, srvv1.AdoptSharedSecretAnnotationKey))
		}
	}
	return errors
}

// noPersistentVolumeLabelAffirmativeKey marks the presence (value "true") of the
// no-persistent-volume label on RVs created without a corresponding PV.
const noPersistentVolumeLabelAffirmativeKey = "sds-replicated-volume.deckhouse.io/no-persistent-volume"

// CheckRVNoPersistentVolumeLabelFromSnapshot verifies that each RV has the
// no-persistent-volume label set to "true".
func CheckRVNoPersistentVolumeLabelFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}
		if rv.Labels == nil {
			errors = append(errors, fmt.Errorf(
				"RV %s has nil labels, expected label %q=%q",
				resourceName, noPersistentVolumeLabelAffirmativeKey, "true",
			))
			continue
		}
		value, exists := rv.Labels[noPersistentVolumeLabelAffirmativeKey]
		if !exists || value != "true" {
			errors = append(errors, fmt.Errorf(
				"RV %s has label %q=%q, expected %q",
				resourceName, noPersistentVolumeLabelAffirmativeKey, value, "true",
			))
		}
	}
	return errors
}

// CheckRVReplicatedStorageClassWithoutPVSnapshot verifies RV replicatedStorageClassName
// without relying on PV storageClassName (PVs may be absent).
// For autoExpected resources, the migrator must have resolved a non-empty
// replicatedStorageClassName from the pool name.
// For manualExpected resources, replicatedStorageClassName must be empty.
func CheckRVReplicatedStorageClassWithoutPVSnapshot(snapshot *RVSnapshot, autoExpected, manualExpected []string) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}

		switch {
		case stringInSlice(resourceName, autoExpected):
			if rv.Spec.ReplicatedStorageClassName == "" {
				errors = append(errors, fmt.Errorf(
					"RV %s has empty spec.replicatedStorageClassName (expected non-empty, migrator should resolve RSC by pool name)",
					resourceName,
				))
			}
		case stringInSlice(resourceName, manualExpected):
			if rv.Spec.ReplicatedStorageClassName != "" {
				errors = append(errors, fmt.Errorf(
					"RV %s has non-empty spec.replicatedStorageClassName=%q (expected empty for Manual-configured resource)",
					resourceName, rv.Spec.ReplicatedStorageClassName,
				))
			}
		default:
			errors = append(errors, fmt.Errorf(
				"RV %s is not expected in auto or manual resource lists",
				resourceName,
			))
		}
	}
	return errors
}

// CheckRVNoPersistentVolumeLabelAbsentFromSnapshot verifies no-persistent-volume label is absent for each RV.
func CheckRVNoPersistentVolumeLabelAbsentFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}
		if rv.Labels == nil {
			continue
		}
		if value, exists := rv.Labels[noPersistentVolumeLabelKey]; exists {
			errors = append(errors, fmt.Errorf(
				"RV %s has unexpected label %q=%q (this scenario expects PV-backed RVs only)",
				resourceName, noPersistentVolumeLabelKey, value,
			))
		}
	}
	return errors
}

func CheckRVSizeMatchesPVFromSnapshot(snapshot *RVSnapshot) []error {
	var errors []error
	for resourceName := range snapshot.UniqueResourceNames {
		rv, ok := snapshot.RVsByName[resourceName]
		if !ok {
			continue
		}
		pv := snapshot.PVsByResource[resourceName]
		if pv == nil {
			errors = append(errors, fmt.Errorf("PV %s not found: this scenario expects PV-backed RVs only", resourceName))
			continue
		}
		qty, ok := pv.Spec.Capacity[corev1.ResourceStorage]
		if !ok || qty.IsZero() {
			errors = append(errors, fmt.Errorf("PV %s has empty spec.capacity.storage", resourceName))
			continue
		}
		expectedSizeRaw := qty.Value()
		actualSize := rv.Spec.Size.Value()
		if actualSize != expectedSizeRaw {
			errors = append(errors, fmt.Errorf(
				"RV %s has spec.size=%d bytes, expected %d bytes",
				resourceName, actualSize, expectedSizeRaw,
			))
		}
	}
	return errors
}

// CompareReplicatedVolumeReplicasWithLinstor compares RVR resources with Linstor state.
func CompareReplicatedVolumeReplicasWithLinstor(ctx context.Context, c client.Client, linstorState *LinstorState) []error {
	slog.Debug("CHECK: Comparing RVR resources with Linstor state")
	var errors []error

	var rvrList srvv1.ReplicatedVolumeReplicaList
	if err := c.List(ctx, &rvrList); err != nil {
		return []error{fmt.Errorf("failed to list RVRs: %w", err)}
	}

	rvrsByResourceNode := make(map[string]srvv1.ReplicatedVolumeReplica, len(rvrList.Items))
	for _, rvr := range rvrList.Items {
		key := fmt.Sprintf("%s/%s", rvr.Spec.ReplicatedVolumeName, rvr.Spec.NodeName)
		rvrsByResourceNode[key] = rvr
	}

	for _, linstorRes := range linstorState.Resources {
		rvrKey := fmt.Sprintf("%s/%s", linstorRes.Name, linstorRes.Node)
		if _, ok := rvrsByResourceNode[rvrKey]; !ok {
			errors = append(errors, fmt.Errorf("RVR not found for Linstor resource %s on node %s", linstorRes.Name, linstorRes.Node))
		}
	}

	slog.Info(fmt.Sprintf("RVR comparison: %d RVRs vs %d Linstor resources", len(rvrList.Items), len(linstorState.Resources)))
	if len(errors) == 0 {
		slog.Debug("PASS: RVR resources match Linstor state")
	}
	return errors
}

type RVRSnapshot struct {
	RVRCount                             int
	LinstorResourcesCount                int
	RVRs                                 []srvv1.ReplicatedVolumeReplica
	LinstorResourceNodeIDs               map[string]bool
	LinstorResourceByPair                map[string]linstorResource
	LinstorHasBackingDiskByPair          map[string]bool
	LinstorVolumeByResourceNode          map[string]linstorVolume
	ExpectedDiskfulStorageByResourceNode map[string]rvrExpectedDiskfulStorage
}

type rvrExpectedDiskfulStorage struct {
	LVMVolumeGroupName         string
	LVMVolumeGroupThinPoolName string
}

func CollectRVRSnapshot(ctx context.Context, c client.Client, linstorState *LinstorState, oldRSPs []RSPBaseline) (*RVRSnapshot, error) {
	var rvrList srvv1.ReplicatedVolumeReplicaList
	if err := c.List(ctx, &rvrList); err != nil {
		return nil, fmt.Errorf("failed to list RVRs: %w", err)
	}

	oldRSPByName := make(map[string]RSPBaseline, len(oldRSPs))
	for _, oldRSP := range oldRSPs {
		oldRSPByName[oldRSP.Name] = oldRSP
	}

	linstorVolumeByResourceNode := make(map[string]linstorVolume, len(linstorState.Volumes))
	expectedDiskfulStorageByResourceNode := make(map[string]rvrExpectedDiskfulStorage)
	for _, vol := range linstorState.Volumes {
		if vol.Resource == "" || vol.Node == "" {
			continue
		}
		resourceNodeKey := normalizeAttachmentPairKey(vol.Resource, vol.Node)

		if existing, exists := linstorVolumeByResourceNode[resourceNodeKey]; exists {
			if existing.BackingDisk != vol.BackingDisk || existing.StoragePool != vol.StoragePool {
				return nil, fmt.Errorf(
					"linstor volume mapping ambiguity for pair %s: conflicting entries (existing storage_pool=%q backing_disk=%q, current storage_pool=%q backing_disk=%q)",
					resourceNodeKey, existing.StoragePool, existing.BackingDisk, vol.StoragePool, vol.BackingDisk,
				)
			}
		} else {
			linstorVolumeByResourceNode[resourceNodeKey] = vol
		}

		if strings.TrimSpace(vol.BackingDisk) == "" {
			continue
		}

		oldRSP, found := oldRSPByName[vol.StoragePool]
		if !found {
			return nil, fmt.Errorf(
				"failed to resolve expected Diskful storage fields for pair %s: storage pool %q is absent in old RSP baselines",
				resourceNodeKey, vol.StoragePool,
			)
		}

		matchingGroups := make([]srvv1.ReplicatedStoragePoolLVMVolumeGroups, 0, 1)
		for _, group := range oldRSP.Spec.LVMVolumeGroups {
			if lvmVolumeGroupBelongsToNode(group.Name, vol.Node) {
				matchingGroups = append(matchingGroups, group)
			}
		}

		if len(matchingGroups) == 0 {
			return nil, fmt.Errorf(
				"failed to resolve expected Diskful storage fields for pair %s: no LVMVolumeGroup in old RSP %q matches node %q",
				resourceNodeKey, oldRSP.Name, vol.Node,
			)
		}
		if len(matchingGroups) > 1 {
			return nil, fmt.Errorf(
				"failed to resolve expected Diskful storage fields for pair %s: multiple LVMVolumeGroups in old RSP %q match node %q",
				resourceNodeKey, oldRSP.Name, vol.Node,
			)
		}

		expected := rvrExpectedDiskfulStorage{
			LVMVolumeGroupName:         matchingGroups[0].Name,
			LVMVolumeGroupThinPoolName: matchingGroups[0].ThinPoolName,
		}
		if existing, exists := expectedDiskfulStorageByResourceNode[resourceNodeKey]; exists {
			if existing != expected {
				return nil, fmt.Errorf(
					"expected Diskful storage mapping ambiguity for pair %s: existing={lvg=%q thinPool=%q} current={lvg=%q thinPool=%q}",
					resourceNodeKey,
					existing.LVMVolumeGroupName,
					existing.LVMVolumeGroupThinPoolName,
					expected.LVMVolumeGroupName,
					expected.LVMVolumeGroupThinPoolName,
				)
			}
			continue
		}
		expectedDiskfulStorageByResourceNode[resourceNodeKey] = expected
	}

	return &RVRSnapshot{
		RVRCount:              len(rvrList.Items),
		LinstorResourcesCount: len(linstorState.Resources),
		RVRs:                  append([]srvv1.ReplicatedVolumeReplica(nil), rvrList.Items...),
		LinstorResourceNodeIDs: func() map[string]bool {
			pairs := make(map[string]bool, len(linstorState.Resources))
			for _, res := range linstorState.Resources {
				if res.Name == "" {
					continue
				}
				// Migrator naming uses uint8 IDs with range limited to 0..31 for RVR names.
				if res.NodeID < 0 || res.NodeID > 31 {
					continue
				}
				key := fmt.Sprintf("%s/%d", res.Name, res.NodeID)
				pairs[key] = true
			}
			return pairs
		}(),
		LinstorResourceByPair: func() map[string]linstorResource {
			pairs := make(map[string]linstorResource, len(linstorState.Resources))
			for _, res := range linstorState.Resources {
				if res.Name == "" || res.NodeID < 0 || res.NodeID > 31 {
					continue
				}
				key := fmt.Sprintf("%s/%d", res.Name, res.NodeID)
				pairs[key] = res
			}
			return pairs
		}(),
		LinstorHasBackingDiskByPair: func() map[string]bool {
			hasBackingDisk := make(map[string]bool)
			for _, vol := range linstorState.Volumes {
				if vol.Resource == "" || vol.Node == "" {
					continue
				}
				key := fmt.Sprintf("%s/%s", strings.ToLower(vol.Resource), strings.ToLower(vol.Node))
				if strings.TrimSpace(vol.BackingDisk) != "" {
					hasBackingDisk[key] = true
				}
			}
			return hasBackingDisk
		}(),
		LinstorVolumeByResourceNode:          linstorVolumeByResourceNode,
		ExpectedDiskfulStorageByResourceNode: expectedDiskfulStorageByResourceNode,
	}, nil
}

func lvmVolumeGroupBelongsToNode(lvgName, nodeName string) bool {
	lvg := strings.ToLower(strings.TrimSpace(lvgName))
	node := strings.ToLower(strings.TrimSpace(nodeName))
	if lvg == "" || node == "" {
		return false
	}
	return strings.HasSuffix(lvg, "-"+sanitizeNodeName(node))
}

// CheckRVRCountMatchesLinstorResourcesFromSnapshot verifies RVR count equals Linstor resources count.
func CheckRVRCountMatchesLinstorResourcesFromSnapshot(snapshot *RVRSnapshot) []error {
	if snapshot.RVRCount != snapshot.LinstorResourcesCount {
		return []error{fmt.Errorf(
			"RVR count mismatch: got %d, expected %d (linstor resources)",
			snapshot.RVRCount, snapshot.LinstorResourcesCount,
		)}
	}
	return nil
}

// CheckRVRNamesMatchLinstorFromSnapshot verifies each RVR name is valid and corresponds
// to an existing (<resourceName>, <nodeID>) pair from saved LINSTOR resources.
func CheckRVRNamesMatchLinstorFromSnapshot(snapshot *RVRSnapshot) []error {
	var errors []error

	for _, rvr := range snapshot.RVRs {
		resourceName, nodeID, err := parseReplicaNameAndNodeID(rvr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		if !snapshot.LinstorResourceNodeIDs[pairKey] {
			errors = append(errors, fmt.Errorf(
				"RVR %s maps to pair %s, but this (resource,nodeID) pair is absent in linstor resources",
				rvr.Name, pairKey,
			))
		}
	}
	return errors
}

func parseReplicaNameAndNodeID(replicaName string) (string, int, error) {
	lastDash := strings.LastIndex(replicaName, "-")
	if lastDash <= 0 {
		return "", 0, fmt.Errorf("RVR %s has invalid name format (expected <replicatedVolumeName>-<id>)", replicaName)
	}
	resourceName := replicaName[:lastDash]
	idPart := replicaName[lastDash+1:]
	nodeID, err := strconv.Atoi(idPart)
	if err != nil || nodeID < 0 || nodeID > 31 {
		return "", 0, fmt.Errorf("RVR %s has invalid nodeID suffix %q (expected integer in range 0..31)", replicaName, idPart)
	}
	return resourceName, nodeID, nil
}

// CheckRVRSpecIdentityFieldsFromSnapshot verifies spec.replicatedVolumeName/spec.nodeName.
func CheckRVRSpecIdentityFieldsFromSnapshot(snapshot *RVRSnapshot) []error {
	var errors []error
	for _, rvr := range snapshot.RVRs {
		resourceName, nodeID, err := parseReplicaNameAndNodeID(rvr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		linstorRes, ok := snapshot.LinstorResourceByPair[pairKey]
		if !ok {
			errors = append(errors, fmt.Errorf("RVR %s maps to absent linstor pair %s", rvr.Name, pairKey))
			continue
		}
		if rvr.Spec.ReplicatedVolumeName != resourceName {
			errors = append(errors, fmt.Errorf(
				"RVR %s has spec.replicatedVolumeName=%q, expected %q",
				rvr.Name, rvr.Spec.ReplicatedVolumeName, resourceName,
			))
		}
		if !strings.EqualFold(rvr.Spec.NodeName, linstorRes.Node) {
			errors = append(errors, fmt.Errorf(
				"RVR %s has spec.nodeName=%q, expected %q from linstor pair %s",
				rvr.Name, rvr.Spec.NodeName, linstorRes.Node, pairKey,
			))
		}
	}
	return errors
}

// CheckRVRSpecTypeMatchesLinstorFromSnapshot verifies type mapping against LINSTOR backing disk presence.
func CheckRVRSpecTypeMatchesLinstorFromSnapshot(snapshot *RVRSnapshot) []error {
	var errors []error
	for _, rvr := range snapshot.RVRs {
		resourceName, nodeID, err := parseReplicaNameAndNodeID(rvr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		linstorRes, ok := snapshot.LinstorResourceByPair[pairKey]
		if !ok {
			errors = append(errors, fmt.Errorf("RVR %s maps to absent linstor pair %s", rvr.Name, pairKey))
			continue
		}
		backingKey := fmt.Sprintf("%s/%s", strings.ToLower(resourceName), strings.ToLower(linstorRes.Node))
		hasBackingDisk := snapshot.LinstorHasBackingDiskByPair[backingKey]
		if hasBackingDisk {
			if rvr.Spec.Type != srvv1.ReplicaTypeDiskful {
				errors = append(errors, fmt.Errorf(
					"RVR %s has spec.type=%s, expected %s because backing_disk is present in linstor",
					rvr.Name, rvr.Spec.Type, srvv1.ReplicaTypeDiskful,
				))
			}
		} else if rvr.Spec.Type == srvv1.ReplicaTypeDiskful {
			errors = append(errors, fmt.Errorf(
				"RVR %s has spec.type=%s, expected non-Diskful because backing_disk is absent in linstor",
				rvr.Name, rvr.Spec.Type,
			))
		}
	}
	return errors
}

// CheckRVRSpecDiskfulStorageFieldsFromSnapshot verifies Diskful replicas have required LVM fields.
func CheckRVRSpecDiskfulStorageFieldsFromSnapshot(snapshot *RVRSnapshot) []error {
	var errors []error
	for _, rvr := range snapshot.RVRs {
		if rvr.Spec.Type != srvv1.ReplicaTypeDiskful {
			continue
		}

		resourceName, nodeID, err := parseReplicaNameAndNodeID(rvr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		linstorRes, ok := snapshot.LinstorResourceByPair[pairKey]
		if !ok {
			errors = append(errors, fmt.Errorf("RVR %s maps to absent linstor pair %s", rvr.Name, pairKey))
			continue
		}

		resourceNodeKey := normalizeAttachmentPairKey(resourceName, linstorRes.Node)
		linstorVol, ok := snapshot.LinstorVolumeByResourceNode[resourceNodeKey]
		if !ok {
			errors = append(errors, fmt.Errorf(
				"RVR %s has Diskful type but no linstor volume found for pair %s",
				rvr.Name, resourceNodeKey,
			))
			continue
		}
		if strings.TrimSpace(linstorVol.BackingDisk) == "" {
			errors = append(errors, fmt.Errorf(
				"RVR %s has Diskful type but linstor volume for pair %s has empty backing_disk",
				rvr.Name, resourceNodeKey,
			))
			continue
		}

		expected, ok := snapshot.ExpectedDiskfulStorageByResourceNode[resourceNodeKey]
		if !ok {
			errors = append(errors, fmt.Errorf(
				"RVR %s has Diskful type but expected storage fields are absent for pair %s",
				rvr.Name, resourceNodeKey,
			))
			continue
		}

		if rvr.Spec.LVMVolumeGroupName != expected.LVMVolumeGroupName {
			errors = append(errors, fmt.Errorf(
				"RVR %s has spec.lvmVolumeGroupName=%q, expected %q for pair %s",
				rvr.Name, rvr.Spec.LVMVolumeGroupName, expected.LVMVolumeGroupName, resourceNodeKey,
			))
		}
		if rvr.Spec.LVMVolumeGroupThinPoolName != expected.LVMVolumeGroupThinPoolName {
			errors = append(errors, fmt.Errorf(
				"RVR %s has spec.lvmVolumeGroupThinPoolName=%q, expected %q for pair %s",
				rvr.Name, rvr.Spec.LVMVolumeGroupThinPoolName, expected.LVMVolumeGroupThinPoolName, resourceNodeKey,
			))
		}
	}
	return errors
}

// CheckRVRSpecNonDiskfulStorageFieldsEmptyFromSnapshot verifies non-Diskful replicas have empty LVM fields.
func CheckRVRSpecNonDiskfulStorageFieldsEmptyFromSnapshot(snapshot *RVRSnapshot) []error {
	var errors []error
	for _, rvr := range snapshot.RVRs {
		if rvr.Spec.Type == srvv1.ReplicaTypeDiskful {
			continue
		}
		if strings.TrimSpace(rvr.Spec.LVMVolumeGroupName) != "" {
			errors = append(errors, fmt.Errorf(
				"RVR %s has non-Diskful type %s but non-empty spec.lvmVolumeGroupName=%q",
				rvr.Name, rvr.Spec.Type, rvr.Spec.LVMVolumeGroupName,
			))
		}
		if strings.TrimSpace(rvr.Spec.LVMVolumeGroupThinPoolName) != "" {
			errors = append(errors, fmt.Errorf(
				"RVR %s has non-Diskful type %s but non-empty spec.lvmVolumeGroupThinPoolName=%q",
				rvr.Name, rvr.Spec.Type, rvr.Spec.LVMVolumeGroupThinPoolName,
			))
		}
	}
	return errors
}

// CompareDRBDResourcesWithLinstor compares DRBDR resources with Linstor state.
func CompareDRBDResourcesWithLinstor(ctx context.Context, c client.Client, linstorState *LinstorState) []error {
	slog.Debug("CHECK: Comparing DRBDR resources with Linstor state")
	var errors []error

	var drbdrList srvv1.DRBDResourceList
	if err := c.List(ctx, &drbdrList); err != nil {
		return []error{fmt.Errorf("failed to list DRBDRs: %w", err)}
	}

	for _, linstorRes := range linstorState.Resources {
		found := false
		for _, drbdr := range drbdrList.Items {
			if drbdr.Labels == nil {
				continue
			}
			rvName, ok := drbdr.Labels[srvv1.ReplicatedVolumeLabelKey]
			if !ok || rvName != linstorRes.Name {
				continue
			}
			if drbdr.Spec.NodeName == linstorRes.Node {
				found = true
				break
			}
		}
		if !found {
			errors = append(errors, fmt.Errorf("DRBDR not found for Linstor resource %s on node %s", linstorRes.Name, linstorRes.Node))
		}
	}

	slog.Info(fmt.Sprintf("DRBDR comparison: %d DRBDRs vs %d Linstor resources", len(drbdrList.Items), len(linstorState.Resources)))
	if len(errors) == 0 {
		slog.Debug("PASS: DRBDR resources match Linstor state")
	}
	return errors
}

type DRBDRSnapshot struct {
	DRBDRCount                          int
	LinstorResourcesCount               int
	DRBDRs                              []srvv1.DRBDResource
	LinstorResourceNodeIDs              map[string]bool
	LinstorResourceByPair               map[string]linstorResource
	LinstorHasBackingDiskByResourceNode map[string]bool
	LLVByName                           map[string]bool
}

func CollectDRBDRSnapshot(ctx context.Context, c client.Client, linstorState *LinstorState) (*DRBDRSnapshot, error) {
	var drbdrList srvv1.DRBDResourceList
	if err := c.List(ctx, &drbdrList); err != nil {
		return nil, fmt.Errorf("failed to list DRBDRs: %w", err)
	}

	var llvList sncv1.LVMLogicalVolumeList
	if err := c.List(ctx, &llvList); err != nil {
		return nil, fmt.Errorf("failed to list LLVs for DRBDR checks: %w", err)
	}

	linstorPairs := make(map[string]bool, len(linstorState.Resources))
	linstorResourceByPair := make(map[string]linstorResource, len(linstorState.Resources))
	for _, res := range linstorState.Resources {
		if res.Name == "" {
			continue
		}
		if res.NodeID < 0 || res.NodeID > 31 {
			continue
		}
		key := fmt.Sprintf("%s/%d", res.Name, res.NodeID)
		linstorPairs[key] = true
		linstorResourceByPair[key] = res
	}

	hasBackingDiskByResourceNode := make(map[string]bool)
	for _, vol := range linstorState.Volumes {
		if vol.Resource == "" || vol.Node == "" {
			continue
		}
		key := normalizeAttachmentPairKey(vol.Resource, vol.Node)
		if strings.TrimSpace(vol.BackingDisk) != "" {
			hasBackingDiskByResourceNode[key] = true
		}
	}

	llvByName := make(map[string]bool, len(llvList.Items))
	for _, llv := range llvList.Items {
		llvByName[llv.Name] = true
	}

	return &DRBDRSnapshot{
		DRBDRCount:                          len(drbdrList.Items),
		LinstorResourcesCount:               len(linstorState.Resources),
		DRBDRs:                              append([]srvv1.DRBDResource(nil), drbdrList.Items...),
		LinstorResourceNodeIDs:              linstorPairs,
		LinstorResourceByPair:               linstorResourceByPair,
		LinstorHasBackingDiskByResourceNode: hasBackingDiskByResourceNode,
		LLVByName:                           llvByName,
	}, nil
}

// CheckDRBDRCountMatchesLinstorResourcesFromSnapshot verifies DRBDR count equals Linstor resources count.
func CheckDRBDRCountMatchesLinstorResourcesFromSnapshot(snapshot *DRBDRSnapshot) []error {
	if snapshot.DRBDRCount != snapshot.LinstorResourcesCount {
		return []error{fmt.Errorf(
			"DRBDR count mismatch: got %d, expected %d (linstor resources)",
			snapshot.DRBDRCount, snapshot.LinstorResourcesCount,
		)}
	}
	return nil
}

// CheckDRBDRNamesMatchLinstorFromSnapshot verifies each DRBDR name maps to
// an existing (<resourceName>, <nodeID>) pair in saved LINSTOR resources.
func CheckDRBDRNamesMatchLinstorFromSnapshot(snapshot *DRBDRSnapshot) []error {
	var errors []error

	for _, drbdr := range snapshot.DRBDRs {
		resourceName, nodeID, err := parseDRBDRNameAndNodeID(drbdr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		if !snapshot.LinstorResourceNodeIDs[pairKey] {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s maps to pair %s, but this (resource,nodeID) pair is absent in linstor resources",
				drbdr.Name, pairKey,
			))
		}
	}

	return errors
}

// CheckDRBDRSpecIdentityDefaultsFromSnapshot verifies DRBDR spec identity and default fields.
func CheckDRBDRSpecIdentityDefaultsFromSnapshot(snapshot *DRBDRSnapshot) []error {
	var errors []error
	for _, drbdr := range snapshot.DRBDRs {
		resourceName, nodeID, err := parseDRBDRNameAndNodeID(drbdr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		linstorRes, ok := snapshot.LinstorResourceByPair[pairKey]
		if !ok {
			errors = append(errors, fmt.Errorf("DRBDR %s maps to absent linstor pair %s", drbdr.Name, pairKey))
			continue
		}

		if drbdr.Spec.NodeID != uint8(nodeID) {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has spec.nodeID=%d, expected %d",
				drbdr.Name, drbdr.Spec.NodeID, nodeID,
			))
		}
		if !strings.EqualFold(drbdr.Spec.NodeName, linstorRes.Node) {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has spec.nodeName=%q, expected %q from linstor pair %s",
				drbdr.Name, drbdr.Spec.NodeName, linstorRes.Node, pairKey,
			))
		}
		if len(drbdr.Spec.SystemNetworks) != 1 || drbdr.Spec.SystemNetworks[0] != "Internal" {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has spec.systemNetworks=%v, expected [Internal]",
				drbdr.Name, drbdr.Spec.SystemNetworks,
			))
		}
		if drbdr.Spec.Maintenance != "" {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has spec.maintenance=%q, expected empty after stage2",
				drbdr.Name, drbdr.Spec.Maintenance,
			))
		}
	}
	return errors
}

// CheckDRBDRSpecTypeMatchesLinstorFromSnapshot verifies DRBDR spec.type mapping against LINSTOR backing disk presence.
func CheckDRBDRSpecTypeMatchesLinstorFromSnapshot(snapshot *DRBDRSnapshot) []error {
	var errors []error
	for _, drbdr := range snapshot.DRBDRs {
		resourceName, nodeID, err := parseDRBDRNameAndNodeID(drbdr.Name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
		linstorRes, ok := snapshot.LinstorResourceByPair[pairKey]
		if !ok {
			errors = append(errors, fmt.Errorf("DRBDR %s maps to absent linstor pair %s", drbdr.Name, pairKey))
			continue
		}

		resourceNodeKey := normalizeAttachmentPairKey(resourceName, linstorRes.Node)
		hasBackingDisk := snapshot.LinstorHasBackingDiskByResourceNode[resourceNodeKey]
		if hasBackingDisk {
			if drbdr.Spec.Type != srvv1.DRBDResourceTypeDiskful {
				errors = append(errors, fmt.Errorf(
					"DRBDR %s has spec.type=%s, expected %s because backing_disk is present in linstor",
					drbdr.Name, drbdr.Spec.Type, srvv1.DRBDResourceTypeDiskful,
				))
			}
		} else if drbdr.Spec.Type != srvv1.DRBDResourceTypeDiskless {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has spec.type=%s, expected %s because backing_disk is absent in linstor",
				drbdr.Name, drbdr.Spec.Type, srvv1.DRBDResourceTypeDiskless,
			))
		}
	}
	return errors
}

// CheckDRBDRSpecStorageFieldsFromSnapshot verifies DRBDR storage fields for Diskful and Diskless types.
func CheckDRBDRSpecStorageFieldsFromSnapshot(snapshot *DRBDRSnapshot) []error {
	var errors []error
	for _, drbdr := range snapshot.DRBDRs {
		if drbdr.Spec.Type == srvv1.DRBDResourceTypeDiskful {
			if drbdr.Spec.Size == nil {
				errors = append(errors, fmt.Errorf("DRBDR %s has Diskful type but empty spec.size", drbdr.Name))
			} else if drbdr.Spec.Size.Value() <= 0 {
				errors = append(errors, fmt.Errorf("DRBDR %s has Diskful type but non-positive spec.size=%d", drbdr.Name, drbdr.Spec.Size.Value()))
			}
			if strings.TrimSpace(drbdr.Spec.LVMLogicalVolumeName) == "" {
				errors = append(errors, fmt.Errorf("DRBDR %s has Diskful type but empty spec.lvmLogicalVolumeName", drbdr.Name))
			} else if !snapshot.LLVByName[drbdr.Spec.LVMLogicalVolumeName] {
				errors = append(errors, fmt.Errorf(
					"DRBDR %s has spec.lvmLogicalVolumeName=%q, but LLV with this name does not exist",
					drbdr.Name, drbdr.Spec.LVMLogicalVolumeName,
				))
			}
			continue
		}

		if drbdr.Spec.Size != nil {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has non-Diskful type %s but non-empty spec.size=%d",
				drbdr.Name, drbdr.Spec.Type, drbdr.Spec.Size.Value(),
			))
		}
		if strings.TrimSpace(drbdr.Spec.LVMLogicalVolumeName) != "" {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has non-Diskful type %s but non-empty spec.lvmLogicalVolumeName=%q",
				drbdr.Name, drbdr.Spec.Type, drbdr.Spec.LVMLogicalVolumeName,
			))
		}
	}
	return errors
}

func parseDRBDRNameAndNodeID(name string) (string, int, error) {
	lastDash := strings.LastIndex(name, "-")
	if lastDash <= 0 {
		return "", 0, fmt.Errorf("DRBDR %s has invalid name format (expected <replicatedVolumeName>-<id>)", name)
	}
	resourceName := name[:lastDash]
	idPart := name[lastDash+1:]
	nodeID, err := strconv.Atoi(idPart)
	if err != nil || nodeID < 0 || nodeID > 31 {
		return "", 0, fmt.Errorf("DRBDR %s has invalid nodeID suffix %q (expected integer in range 0..31)", name, idPart)
	}
	return resourceName, nodeID, nil
}

// CheckDRBDROwnerReferenceFromSnapshot verifies DRBDR ownerRef points to ReplicatedVolumeReplica
// and ownerRef.name equals DRBDR name.
func CheckDRBDROwnerReferenceFromSnapshot(snapshot *DRBDRSnapshot) []error {
	var errors []error
	for _, drbdr := range snapshot.DRBDRs {
		if len(drbdr.OwnerReferences) != 1 {
			errors = append(errors, fmt.Errorf("DRBDR %s must have exactly 1 ownerReference, got %d", drbdr.Name, len(drbdr.OwnerReferences)))
			continue
		}

		ref := drbdr.OwnerReferences[0]
		if ref.Kind != "ReplicatedVolumeReplica" {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has ownerReference.kind=%q, expected %q",
				drbdr.Name, ref.Kind, "ReplicatedVolumeReplica",
			))
			continue
		}
		if ref.Controller == nil || !*ref.Controller {
			errors = append(errors, fmt.Errorf("DRBDR %s has ownerReference.controller!=true", drbdr.Name))
			continue
		}
		if ref.Name != drbdr.Name {
			errors = append(errors, fmt.Errorf(
				"DRBDR %s has ownerReference.name=%q, expected %q",
				drbdr.Name, ref.Name, drbdr.Name,
			))
		}
	}
	return errors
}

type LLVSnapshot struct {
	LLVCount                           int
	LinstorVolumesWithBackingDiskCount int
	LLVs                               []sncv1.LVMLogicalVolume
	RVRNames                           map[string]bool
	LinstorResourceByPair              map[string]linstorResource
	ExpectedSpecByResourceNode         map[string]llvExpectedSpec
}

type llvExpectedSpec struct {
	ActualLVNameOnTheNode string
	LVMVolumeGroupName    string
	ThinPoolName          string
}

func CollectLLVSnapshot(ctx context.Context, c client.Client, linstorState *LinstorState, oldRSPs []RSPBaseline) (*LLVSnapshot, error) {
	var llvList sncv1.LVMLogicalVolumeList
	if err := c.List(ctx, &llvList); err != nil {
		return nil, fmt.Errorf("failed to list LLVs: %w", err)
	}
	var rvrList srvv1.ReplicatedVolumeReplicaList
	if err := c.List(ctx, &rvrList); err != nil {
		return nil, fmt.Errorf("failed to list RVRs for LLV name check: %w", err)
	}
	rvrNames := make(map[string]bool, len(rvrList.Items))
	for _, rvr := range rvrList.Items {
		rvrNames[rvr.Name] = true
	}

	linstorResourceByPair := make(map[string]linstorResource, len(linstorState.Resources))
	for _, res := range linstorState.Resources {
		if res.Name == "" || res.NodeID < 0 || res.NodeID > 31 {
			continue
		}
		key := fmt.Sprintf("%s/%d", res.Name, res.NodeID)
		linstorResourceByPair[key] = res
	}

	oldRSPByName := make(map[string]RSPBaseline, len(oldRSPs))
	for _, oldRSP := range oldRSPs {
		oldRSPByName[oldRSP.Name] = oldRSP
	}

	expectedSpecByResourceNode := make(map[string]llvExpectedSpec)
	linstorWithBackingDisk := 0
	for _, vol := range linstorState.Volumes {
		if strings.TrimSpace(vol.BackingDisk) != "" {
			linstorWithBackingDisk++

			oldRSP, found := oldRSPByName[vol.StoragePool]
			if !found {
				return nil, fmt.Errorf(
					"failed to resolve expected LLV spec for pair %s/%s: storage pool %q is absent in old RSP baselines",
					vol.Resource, vol.Node, vol.StoragePool,
				)
			}

			matchingGroups := make([]srvv1.ReplicatedStoragePoolLVMVolumeGroups, 0, 1)
			for _, group := range oldRSP.Spec.LVMVolumeGroups {
				if lvmVolumeGroupBelongsToNode(group.Name, vol.Node) {
					matchingGroups = append(matchingGroups, group)
				}
			}
			if len(matchingGroups) == 0 {
				return nil, fmt.Errorf(
					"failed to resolve expected LLV spec for pair %s/%s: no LVMVolumeGroup in old RSP %q matches node %q",
					vol.Resource, vol.Node, oldRSP.Name, vol.Node,
				)
			}
			if len(matchingGroups) > 1 {
				return nil, fmt.Errorf(
					"failed to resolve expected LLV spec for pair %s/%s: multiple LVMVolumeGroups in old RSP %q match node %q",
					vol.Resource, vol.Node, oldRSP.Name, vol.Node,
				)
			}

			actualLVName := backingDiskBaseName(vol.BackingDisk)
			if actualLVName == "" {
				return nil, fmt.Errorf(
					"failed to resolve expected LLV spec for pair %s/%s: backing_disk %q has empty basename",
					vol.Resource, vol.Node, vol.BackingDisk,
				)
			}

			resourceNodeKey := normalizeAttachmentPairKey(vol.Resource, vol.Node)
			expected := llvExpectedSpec{
				ActualLVNameOnTheNode: actualLVName,
				LVMVolumeGroupName:    matchingGroups[0].Name,
				ThinPoolName:          matchingGroups[0].ThinPoolName,
			}
			if existing, exists := expectedSpecByResourceNode[resourceNodeKey]; exists && existing != expected {
				return nil, fmt.Errorf(
					"expected LLV spec mapping ambiguity for pair %s: existing={actualLV=%q lvg=%q thinPool=%q} current={actualLV=%q lvg=%q thinPool=%q}",
					resourceNodeKey,
					existing.ActualLVNameOnTheNode,
					existing.LVMVolumeGroupName,
					existing.ThinPoolName,
					expected.ActualLVNameOnTheNode,
					expected.LVMVolumeGroupName,
					expected.ThinPoolName,
				)
			}
			expectedSpecByResourceNode[resourceNodeKey] = expected
		}
	}

	return &LLVSnapshot{
		LLVCount:                           len(llvList.Items),
		LinstorVolumesWithBackingDiskCount: linstorWithBackingDisk,
		LLVs:                               append([]sncv1.LVMLogicalVolume(nil), llvList.Items...),
		RVRNames:                           rvrNames,
		LinstorResourceByPair:              linstorResourceByPair,
		ExpectedSpecByResourceNode:         expectedSpecByResourceNode,
	}, nil
}

func backingDiskBaseName(backingDisk string) string {
	trimmed := strings.TrimSpace(backingDisk)
	if trimmed == "" {
		return ""
	}
	lastSlash := strings.LastIndex(trimmed, "/")
	if lastSlash < 0 {
		return trimmed
	}
	if lastSlash+1 >= len(trimmed) {
		return ""
	}
	return trimmed[lastSlash+1:]
}

func expectedLLVSpecForLLV(snapshot *LLVSnapshot, llv sncv1.LVMLogicalVolume) (string, llvExpectedSpec, error) {
	lastDash := strings.LastIndex(llv.Name, "-")
	if lastDash <= 0 {
		return "", llvExpectedSpec{}, fmt.Errorf("LLV %s has invalid name format (expected <rvrName>-<suffix>)", llv.Name)
	}

	rvrName := llv.Name[:lastDash]
	resourceName, nodeID, err := parseReplicaNameAndNodeID(rvrName)
	if err != nil {
		return "", llvExpectedSpec{}, fmt.Errorf("LLV %s has invalid RVR prefix %q: %w", llv.Name, rvrName, err)
	}

	pairKey := fmt.Sprintf("%s/%d", resourceName, nodeID)
	linstorRes, ok := snapshot.LinstorResourceByPair[pairKey]
	if !ok {
		return "", llvExpectedSpec{}, fmt.Errorf("LLV %s maps to absent linstor pair %s", llv.Name, pairKey)
	}

	resourceNodeKey := normalizeAttachmentPairKey(resourceName, linstorRes.Node)
	expected, ok := snapshot.ExpectedSpecByResourceNode[resourceNodeKey]
	if !ok {
		return "", llvExpectedSpec{}, fmt.Errorf("LLV %s maps to pair %s without expected LLV spec mapping", llv.Name, resourceNodeKey)
	}

	return resourceNodeKey, expected, nil
}

// CheckLLVCountMatchesLinstorBackingDiskVolumesFromSnapshot verifies LLV count equals count of LINSTOR volumes with backing_disk.
func CheckLLVCountMatchesLinstorBackingDiskVolumesFromSnapshot(snapshot *LLVSnapshot) []error {
	if snapshot.LLVCount != snapshot.LinstorVolumesWithBackingDiskCount {
		return []error{fmt.Errorf(
			"LLV count mismatch: got %d, expected %d (linstor volumes with non-empty backing_disk)",
			snapshot.LLVCount, snapshot.LinstorVolumesWithBackingDiskCount,
		)}
	}
	return nil
}

// CheckLLVNamesMatchRVRPrefixFromSnapshot verifies LLV names follow <rvrName>-<suffix>,
// where <rvrName> exists among current RVR names and <suffix> is non-empty.
func CheckLLVNamesMatchRVRPrefixFromSnapshot(snapshot *LLVSnapshot) []error {
	var errors []error
	for _, llv := range snapshot.LLVs {
		lastDash := strings.LastIndex(llv.Name, "-")
		if lastDash <= 0 {
			errors = append(errors, fmt.Errorf("LLV %s has invalid name format (expected <rvrName>-<suffix>)", llv.Name))
			continue
		}

		rvrName := llv.Name[:lastDash]
		suffix := llv.Name[lastDash+1:]
		if strings.TrimSpace(suffix) == "" {
			errors = append(errors, fmt.Errorf("LLV %s has empty suffix part after rvrName prefix", llv.Name))
			continue
		}
		if !snapshot.RVRNames[rvrName] {
			errors = append(errors, fmt.Errorf("LLV %s has prefix %q which is not an existing RVR name", llv.Name, rvrName))
		}
	}
	return errors
}

// CheckLLVSpecIdentityAndPlacementFieldsFromSnapshot verifies LLV spec identity and placement fields.
func CheckLLVSpecIdentityAndPlacementFieldsFromSnapshot(snapshot *LLVSnapshot) []error {
	var errors []error
	seenByResourceNode := make(map[string]bool, len(snapshot.LLVs))

	for _, llv := range snapshot.LLVs {
		resourceNodeKey, expected, err := expectedLLVSpecForLLV(snapshot, llv)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		seenByResourceNode[resourceNodeKey] = true

		if llv.Spec.ActualLVNameOnTheNode != expected.ActualLVNameOnTheNode {
			errors = append(errors, fmt.Errorf(
				"LLV %s has spec.actualLVNameOnTheNode=%q, expected %q for pair %s",
				llv.Name, llv.Spec.ActualLVNameOnTheNode, expected.ActualLVNameOnTheNode, resourceNodeKey,
			))
		}
		if llv.Spec.LVMVolumeGroupName != expected.LVMVolumeGroupName {
			errors = append(errors, fmt.Errorf(
				"LLV %s has spec.lvmVolumeGroupName=%q, expected %q for pair %s",
				llv.Name, llv.Spec.LVMVolumeGroupName, expected.LVMVolumeGroupName, resourceNodeKey,
			))
		}
	}

	for resourceNodeKey := range snapshot.ExpectedSpecByResourceNode {
		if !seenByResourceNode[resourceNodeKey] {
			errors = append(errors, fmt.Errorf("LLV not found for expected pair %s", resourceNodeKey))
		}
	}

	return errors
}

// CheckLLVSpecTypeAndThinFieldsFromSnapshot verifies LLV type and thin-pool fields.
func CheckLLVSpecTypeAndThinFieldsFromSnapshot(snapshot *LLVSnapshot) []error {
	var errors []error
	for _, llv := range snapshot.LLVs {
		resourceNodeKey, expected, err := expectedLLVSpecForLLV(snapshot, llv)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		if expected.ThinPoolName != "" {
			if llv.Spec.Type != "Thin" {
				errors = append(errors, fmt.Errorf(
					"LLV %s has spec.type=%q, expected %q for pair %s",
					llv.Name, llv.Spec.Type, "Thin", resourceNodeKey,
				))
			}
			if llv.Spec.Thin == nil {
				errors = append(errors, fmt.Errorf("LLV %s has Thin type expectation but spec.thin is nil (pair %s)", llv.Name, resourceNodeKey))
				continue
			}
			if llv.Spec.Thin.PoolName != expected.ThinPoolName {
				errors = append(errors, fmt.Errorf(
					"LLV %s has spec.thin.poolName=%q, expected %q for pair %s",
					llv.Name, llv.Spec.Thin.PoolName, expected.ThinPoolName, resourceNodeKey,
				))
			}
			continue
		}

		if llv.Spec.Type != "Thick" {
			errors = append(errors, fmt.Errorf(
				"LLV %s has spec.type=%q, expected %q for pair %s",
				llv.Name, llv.Spec.Type, "Thick", resourceNodeKey,
			))
		}
		if llv.Spec.Thin != nil {
			errors = append(errors, fmt.Errorf(
				"LLV %s has Thick type expectation but non-nil spec.thin for pair %s",
				llv.Name, resourceNodeKey,
			))
		}
	}

	return errors
}

// CheckLLVSpecSizeFieldFromSnapshot verifies LLV spec.size is a positive quantity.
func CheckLLVSpecSizeFieldFromSnapshot(snapshot *LLVSnapshot) []error {
	var errors []error
	for _, llv := range snapshot.LLVs {
		if _, _, err := expectedLLVSpecForLLV(snapshot, llv); err != nil {
			errors = append(errors, err)
			continue
		}

		sizeRaw := strings.TrimSpace(llv.Spec.Size)
		if sizeRaw == "" {
			errors = append(errors, fmt.Errorf("LLV %s has empty spec.size", llv.Name))
			continue
		}

		qty, err := resource.ParseQuantity(sizeRaw)
		if err != nil {
			errors = append(errors, fmt.Errorf("LLV %s has invalid spec.size=%q: %v", llv.Name, llv.Spec.Size, err))
			continue
		}
		if qty.Sign() <= 0 {
			errors = append(errors, fmt.Errorf("LLV %s has non-positive spec.size=%q", llv.Name, llv.Spec.Size))
		}
	}

	return errors
}

// CheckLLVOwnerReferenceFromSnapshot verifies LLV ownerRef points to ReplicatedVolumeReplica
// and ownerRef.name matches the beginning of LLV name.
func CheckLLVOwnerReferenceFromSnapshot(snapshot *LLVSnapshot) []error {
	var errors []error
	for _, llv := range snapshot.LLVs {
		if len(llv.OwnerReferences) != 1 {
			errors = append(errors, fmt.Errorf("LLV %s must have exactly 1 ownerReference, got %d", llv.Name, len(llv.OwnerReferences)))
			continue
		}

		ref := llv.OwnerReferences[0]
		if ref.Kind != "ReplicatedVolumeReplica" {
			errors = append(errors, fmt.Errorf(
				"LLV %s has ownerReference.kind=%q, expected %q",
				llv.Name, ref.Kind, "ReplicatedVolumeReplica",
			))
			continue
		}
		if ref.Controller == nil || !*ref.Controller {
			errors = append(errors, fmt.Errorf("LLV %s has ownerReference.controller!=true", llv.Name))
			continue
		}
		if !strings.HasPrefix(llv.Name, ref.Name) {
			errors = append(errors, fmt.Errorf(
				"LLV %s has ownerReference.name=%q which is not a prefix of LLV name",
				llv.Name, ref.Name,
			))
		}
	}
	return errors
}

// CompareLVMLogicalVolumesWithLinstor compares LLV resources with Linstor state.
func CompareLVMLogicalVolumesWithLinstor(ctx context.Context, c client.Client, linstorState *LinstorState) []error {
	slog.Debug("CHECK: Comparing LLV resources with Linstor state")
	var errors []error

	var drbdrList srvv1.DRBDResourceList
	if err := c.List(ctx, &drbdrList); err != nil {
		return []error{fmt.Errorf("failed to list DRBDRs for LLV check: %w", err)}
	}

	var llvList sncv1.LVMLogicalVolumeList
	if err := c.List(ctx, &llvList); err != nil {
		return []error{fmt.Errorf("failed to list LLVs: %w", err)}
	}

	llvsByName := make(map[string]sncv1.LVMLogicalVolume, len(llvList.Items))
	for _, llv := range llvList.Items {
		llvsByName[llv.Name] = llv
	}

	for _, linstorRes := range linstorState.Resources {
		var matchedDRBDR *srvv1.DRBDResource
		for _, drbdr := range drbdrList.Items {
			if drbdr.Labels == nil {
				continue
			}
			rvName, ok := drbdr.Labels[srvv1.ReplicatedVolumeLabelKey]
			if !ok || rvName != linstorRes.Name {
				continue
			}
			if drbdr.Spec.NodeName == linstorRes.Node {
				drbdrCopy := drbdr
				matchedDRBDR = &drbdrCopy
				break
			}
		}
		if matchedDRBDR == nil {
			// DRBDR comparison is covered in CompareDRBDResourcesWithLinstor.
			continue
		}

		// TODO: For Diskless/TieBreaker we will extend criteria when their comparison logic is finalized.
		llvName := matchedDRBDR.Spec.LVMLogicalVolumeName
		if llvName == "" {
			continue
		}

		llv, ok := llvsByName[llvName]
		if !ok {
			errors = append(errors, fmt.Errorf("LLV %s not found for Linstor resource %s on node %s", llvName, linstorRes.Name, linstorRes.Node))
			continue
		}
		if llv.Labels == nil || llv.Labels[srvv1.ReplicatedVolumeLabelKey] != linstorRes.Name {
			errors = append(errors, fmt.Errorf("LLV %s has unexpected replicated-volume label for resource %s", llvName, linstorRes.Name))
		}
		if llv.Status == nil || llv.Status.Phase != "Created" {
			errors = append(errors, fmt.Errorf("LLV %s is not ready for resource %s (phase: %v)", llvName, linstorRes.Name, llv.Status))
		}
	}

	slog.Info(fmt.Sprintf("LLV comparison: %d LLVs vs %d Linstor resources", len(llvList.Items), len(linstorState.Resources)))
	if len(errors) == 0 {
		slog.Debug("PASS: LLV resources match Linstor state")
	}
	return errors
}

// UpdateModulePullOverrideAndWait updates mpo/sds-replicated-volume spec.imageTag
// and waits until module/sds-replicated-volume reaches Ready phase.
func UpdateModulePullOverrideAndWait(ctx context.Context, c client.Client, imageTag string) error {
	if imageTag == "" {
		return fmt.Errorf("imageTag must be provided for ModulePullOverride update")
	}

	slog.Debug(fmt.Sprintf("CHECK: Updating ModulePullOverride %s imageTag to %s", moduleNameSRV, imageTag))

	mpo := &unstructured.Unstructured{}
	mpo.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1alpha2",
		Kind:    "ModulePullOverride",
	})

	if err := c.Get(ctx, client.ObjectKey{Name: moduleNameSRV}, mpo); err != nil {
		return fmt.Errorf("failed to get ModulePullOverride %s: %w", moduleNameSRV, err)
	}

	if err := unstructured.SetNestedField(mpo.Object, imageTag, "spec", "imageTag"); err != nil {
		return fmt.Errorf("failed to set spec.imageTag for ModulePullOverride %s: %w", moduleNameSRV, err)
	}
	if err := c.Update(ctx, mpo); err != nil {
		return fmt.Errorf("failed to update ModulePullOverride %s: %w", moduleNameSRV, err)
	}

	slog.Info(fmt.Sprintf("ModulePullOverride %s updated, waiting for module phase Ready", moduleNameSRV))

	module := &unstructured.Unstructured{}
	module.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1alpha1",
		Kind:    "Module",
	})

	deadline := time.Now().Add(MigrationWaitTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for module %s to become Ready after %v", moduleNameSRV, MigrationWaitTimeout)
		}

		// By request, delay must be at the beginning of each poll iteration.
		time.Sleep(10 * time.Second)

		if err := c.Get(ctx, client.ObjectKey{Name: moduleNameSRV}, module); err != nil {
			if apierrors.IsNotFound(err) {
				slog.Info(fmt.Sprintf("Module %s not found yet, waiting...", moduleNameSRV))
				continue
			}
			return fmt.Errorf("failed to get Module %s: %w", moduleNameSRV, err)
		}

		phase, found, err := unstructured.NestedString(module.Object, "status", "phase")
		if err != nil {
			return fmt.Errorf("failed to read status.phase for module %s: %w", moduleNameSRV, err)
		}
		if !found || phase == "" {
			slog.Info(fmt.Sprintf("Module %s has empty status.phase, waiting...", moduleNameSRV))
			continue
		}
		if phase == "Ready" {
			slog.Debug(fmt.Sprintf("PASS: Module %s is Ready after MPO update", moduleNameSRV))
			return nil
		}

		slog.Info(fmt.Sprintf("Module %s phase is %s, waiting for Ready...", moduleNameSRV, phase))
	}
}

// stringInSlice returns true if s is present in list.
func stringInSlice(s string, list []string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// VerifyNoReplicatedStorageMetadataBackupsExist checks that no
// ReplicatedStorageMetadataBackup resources remain in the cluster.
func VerifyNoReplicatedStorageMetadataBackupsExist(ctx context.Context, c client.Client) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "storage.deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ReplicatedStorageMetadataBackupList",
	})

	if err := c.List(ctx, list); err != nil {
		// CRD may already be removed — treat as success.
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to list replicatedstoragemetadatabackups: %w", err)
	}

	if len(list.Items) > 0 {
		var names []string
		for _, item := range list.Items {
			names = append(names, item.GetName())
		}
		return fmt.Errorf("expected 0 replicatedstoragemetadatabackups, found %d: %v", len(list.Items), names)
	}
	return nil
}

// VerifyNoLinstorCRDExists checks that the legacy Linstor CRD
// "nodes.internal.linstor.linbit.com" has been deleted.
func VerifyNoLinstorCRDExists(ctx context.Context, c client.Client) error {
	crdName := "nodes.internal.linstor.linbit.com"
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "apiextensions.k8s.io",
		Version: "v1",
		Kind:    "CustomResourceDefinition",
	})
	err := c.Get(ctx, types.NamespacedName{Name: crdName}, crd)
	if err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("failed to get CRD %s: %w", crdName, err)
	}
	return fmt.Errorf("CRD %s still exists in the cluster", crdName)
}
