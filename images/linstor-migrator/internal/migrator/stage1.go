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

package migrator

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/kubeutils"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstordb"
)

// AutoReplicatedStoragePoolName returns the deterministic Kubernetes name for a migration
// ReplicatedStoragePool derived from a LINSTOR storage pool name.
func AutoReplicatedStoragePoolName(linstorPoolName string) string {
	return config.AutoReplicatedStoragePoolNamePrefix + slugForAutoRSPName(linstorPoolName)
}

func slugForAutoRSPName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "pool"
	}
	if len(out) > config.AutoReplicatedStoragePoolNameSlugMaxLen {
		out = out[:config.AutoReplicatedStoragePoolNameSlugMaxLen]
		out = strings.TrimRight(out, "-")
	}
	if out == "" {
		return "pool"
	}
	return out
}

// waitForLinstorControllerRemoval waits for the linstor-controller deployment
// to be removed from the d8-sds-replicated-volume namespace (max 10 minutes).
func (m *Migrator) waitForLinstorControllerRemoval(ctx context.Context) error {
	const (
		timeout  = 10 * time.Minute
		interval = 2 * time.Second
	)

	m.log.Info("waiting for linstor-controller deployment removal",
		"timeout", timeout, "namespace", config.ModuleNamespace)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("timeout waiting for linstor-controller deployment removal after %s", timeout)
			}
			return ctx.Err()
		case <-ticker.C:
			deployment := &appsv1.Deployment{}
			err := m.client.Get(ctx, types.NamespacedName{
				Namespace: config.ModuleNamespace,
				Name:      "linstor-controller",
			}, deployment)

			if apierrors.IsNotFound(err) {
				m.log.Info("linstor-controller deployment is gone, proceeding")
				return nil
			}

			if err != nil {
				if kubeutils.IsTransientAPIError(err) {
					m.log.Warn("transient error checking linstor-controller deployment, will retry on next tick", "err", err)
					continue
				}
				return fmt.Errorf("failed to check linstor-controller deployment: %w", err)
			}

			m.log.Debug("linstor-controller deployment still exists, waiting...")
		}
	}
}

// runStage1 performs resource migration from LINSTOR to new control-plane CRs.
// It waits for linstor-controller removal, loads LINSTOR data, classifies resources,
// and migrates them in the correct order.
func (m *Migrator) runStage1(ctx context.Context) error {
	m.log.Info("stage 1: starting resource migration")

	if err := m.updateMigrationStateRetrying(ctx, config.StateStage1Started); err != nil {
		return err
	}

	// 1. Wait for linstor-controller removal
	if err := m.waitForLinstorControllerRemoval(ctx); err != nil {
		return err
	}

	// 2. If LINSTOR CRDs are gone, skip the rest of stage 1.
	found, err := m.crdExistsWithRetry(ctx, config.LinstorCRDName, "LINSTOR")
	if err != nil {
		return err
	}
	if !found {
		m.log.Info("LINSTOR not found in cluster, no resource migration needed")
		if err := m.updateMigrationStateRetrying(ctx, config.StateStage1Completed); err != nil {
			return err
		}
		return nil
	}
	m.log.Debug("LINSTOR CRD found", "crd", config.LinstorCRDName)

	// 3. Load LINSTOR database
	db, err := linstordb.Init(ctx, m.client, m.retryTransient)
	if err != nil {
		return fmt.Errorf("failed to initialize LINSTOR database: %w", err)
	}
	m.log.Info("LINSTOR database loaded", "resources", len(db.Resources), "resourceDefinitions", len(db.ResourceDefinitions), "resourceGroups", len(db.ResourceGroups))

	// 4. Load PVs for analysis
	pvs := &corev1.PersistentVolumeList{}
	if err := m.retryTransient(ctx, "list PersistentVolumes", func() error {
		return m.client.List(ctx, pvs)
	}); err != nil {
		return fmt.Errorf("failed to list PersistentVolumes: %w", err)
	}

	// Create PV map by resource name (lowercase), filtering only PVs with replicated CSI driver.
	pvMap := make(map[string]corev1.PersistentVolume)
	for _, pv := range pvs.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == config.CSIDriverReplicated {
			pvMap[strings.ToLower(pv.Name)] = pv
		}
	}

	// 5. Classify resources
	var (
		// resourcesWithPV holds resource names (PV names) that exist in both LINSTOR and the cluster.
		resourcesWithPV []string
		// resourcesWithoutPV holds resource names (PV names) found in LINSTOR but missing from the cluster.
		resourcesWithoutPV []string
		// resourcesWithoutRD holds resource names (PV names) found in the cluster but missing ResourceDefinitions in LINSTOR.
		resourcesWithoutRD []string
	)

	for resName := range db.Resources {
		_, hasPV := pvMap[resName]
		_, hasRD := db.ResourceDefinitions[resName]

		switch {
		case !hasRD:
			resourcesWithoutRD = append(resourcesWithoutRD, resName)
		case hasPV:
			resourcesWithPV = append(resourcesWithPV, resName)
		default:
			resourcesWithoutPV = append(resourcesWithoutPV, resName)
		}
	}

	// 6. Log classification results
	m.log.Info("resource classification complete",
		"withPV", len(resourcesWithPV),
		"withoutPV", len(resourcesWithoutPV),
		"withoutRD", len(resourcesWithoutRD))

	if len(resourcesWithoutPV) > 0 {
		m.log.Info("resources without PV", "names", resourcesWithoutPV)
	}
	if len(resourcesWithoutRD) > 0 {
		m.log.Info("resources without ResourceDefinition (will be skipped)", "names", resourcesWithoutRD)
	}

	// 7. List VolumeAttachments, LVMVolumeGroups; ensure migration RSP per LINSTOR pool.
	vaList := &storagev1.VolumeAttachmentList{}
	if err := m.retryTransient(ctx, "list VolumeAttachments", func() error {
		return m.client.List(ctx, vaList)
	}); err != nil {
		return fmt.Errorf("failed to list VolumeAttachments: %w", err)
	}
	// Filter VolumeAttachments to only include those for our CSI driver.
	replicatedVAList := make([]storagev1.VolumeAttachment, 0, len(vaList.Items))
	for _, va := range vaList.Items {
		if va.Spec.Attacher == config.CSIDriverReplicated {
			replicatedVAList = append(replicatedVAList, va)
		}
	}
	m.log.Debug("replicated VolumeAttachments", "count", len(replicatedVAList))

	lvgList := &sncv1alpha1.LVMVolumeGroupList{}
	if err := m.retryTransient(ctx, "list LVMVolumeGroups", func() error {
		return m.client.List(ctx, lvgList)
	}); err != nil {
		return fmt.Errorf("failed to list LVMVolumeGroups: %w", err)
	}
	lvgs := make(map[string]sncv1alpha1.LVMVolumeGroup, len(lvgList.Items))
	for _, lvg := range lvgList.Items {
		lvgs[lvg.Name] = lvg
	}
	m.log.Debug("loaded LVMVolumeGroups", "count", len(lvgs))

	// Collect distinct LINSTOR storage pool names (from ResourceGroup via GetPoolName) across every
	// volume we will migrate. Each pool is ensured exactly one auto-created ReplicatedStoragePool
	// (linstor-auto-*) before per-volume migration, regardless of how many volumes share the pool.
	uniquePools := make(map[string]struct{})
	for _, resName := range resourcesWithPV {
		pn, err := db.GetPoolName(resName)
		if err != nil {
			return fmt.Errorf("pool name for resource %q: %w", resName, err)
		}
		uniquePools[pn] = struct{}{}
	}
	for _, resName := range resourcesWithoutPV {
		pn, err := db.GetPoolName(resName)
		if err != nil {
			return fmt.Errorf("pool name for resource %q: %w", resName, err)
		}
		uniquePools[pn] = struct{}{}
	}

	// Create ReplicatedStoragePool per LINSTOR pool
	migrationRSPByPool := make(map[string]*srvv1alpha1.ReplicatedStoragePool, len(uniquePools))
	for poolName := range uniquePools {
		rsp, err := m.ensureMigrationRSP(ctx, poolName, db, lvgs)
		if err != nil {
			return fmt.Errorf("ensure migration ReplicatedStoragePool for LINSTOR pool %q: %w", poolName, err)
		}
		if err := validateRSPReferencedLVMVolumeGroupsExist(rsp, lvgs, poolName); err != nil {
			return err
		}
		migrationRSPByPool[poolName] = rsp
	}

	// 8. Migrate: first resources with PV, then without PV
	for _, resName := range resourcesWithPV {
		pv := pvMap[resName]
		if err := m.migrateResource(ctx, resName, &pv, db, migrationRSPByPool, replicatedVAList, lvgs); err != nil {
			return fmt.Errorf("failed to migrate resource %s (with PV): %w", resName, err)
		}
	}

	for _, resName := range resourcesWithoutPV {
		if err := m.migrateResource(ctx, resName, nil, db, migrationRSPByPool, replicatedVAList, lvgs); err != nil {
			return fmt.Errorf("failed to migrate resource %s (without PV): %w", resName, err)
		}
	}

	// 9. Mark stage 1 completed.
	if err := m.updateMigrationStateRetrying(ctx, config.StateStage1Completed); err != nil {
		return err
	}

	m.log.Info("stage 1: resource migration completed")
	return nil
}

type replicaInfo struct {
	nodeName     string
	nodeID       uint8
	replicaType  srvv1alpha1.ReplicaType
	rvrName      string
	llvName      string
	lvgName      string
	thinPoolName string
}

// migrateResource migrates one LINSTOR resource to new control-plane CRs.
// pv may be nil if the resource has no corresponding PersistentVolume.
func (m *Migrator) migrateResource(
	ctx context.Context,
	resName string,
	pv *corev1.PersistentVolume,
	db *linstordb.LinstorDB,
	migrationRSPByPool map[string]*srvv1alpha1.ReplicatedStoragePool,
	vaList []storagev1.VolumeAttachment,
	lvgs map[string]sncv1alpha1.LVMVolumeGroup,
) error {
	log := m.log.With("resource", resName)
	log.Info("migrating resource")

	poolName, err := db.GetPoolName(resName)
	if err != nil {
		return fmt.Errorf("failed to get pool name: %w", err)
	}

	rsp, ok := migrationRSPByPool[poolName]
	if !ok || rsp == nil {
		return fmt.Errorf("migration ReplicatedStoragePool not built for LINSTOR pool %q", poolName)
	}

	size, err := db.GetSize(pv, resName)
	if err != nil {
		log.Warn("skipping resource: cannot determine volume size", "err", err)
		return nil
	}
	if size.IsZero() {
		log.Warn("skipping resource: volume size is zero")
		return nil
	}

	sharedSecret, err := db.GetSharedSecret(resName)
	if err != nil {
		return fmt.Errorf("failed to get shared secret: %w", err)
	}

	// Truncate shared secret for logging to avoid exposing sensitive data.
	sharedSecretPrefix := sharedSecret
	if len(sharedSecretPrefix) > 5 {
		sharedSecretPrefix = sharedSecretPrefix[:5] + "..."
	}

	log.Debug("determined resource parameters",
		"size", size.String(),
		"hasPV", pv != nil,
		"sharedSecretPrefix", sharedSecretPrefix,
		"poolName", poolName,
		"migrationRSP", rsp.Name)

	// Migration workflow for each LINSTOR resource:
	// Step 1: Create LLV and DRBDR for each LINSTOR resource.
	// Step 2: Create RVR.
	// Step 3: Set ownerRef on RVR for LLV and DRBDR.
	// Step 4: Calculate FTT and GMDR based on replica types; create ReplicatedVolume (adopt-rvr).
	// Step 5: Create RVA if VolumeAttachment exists.

	linstorResources, ok := db.Resources[resName]
	if !ok {
		return fmt.Errorf("LINSTOR resources not found for %s", resName)
	}

	// Step 1: Create LLV and DRBDR for each LINSTOR resource.
	replicas := make([]replicaInfo, 0, len(linstorResources))
	for _, lr := range linstorResources {
		nodeName := strings.ToLower(lr.Spec.NodeName)
		replicaType := resourceFlagToReplicaType(lr.Spec.ResourceFlags)

		nodeID, err := db.GetNodeID(resName, lr.Spec.NodeName)
		if err != nil {
			return fmt.Errorf("failed to get node ID for node %q: %w", nodeName, err)
		}

		rvrName := srvv1alpha1.FormatReplicatedVolumeReplicaName(resName, uint8(nodeID))

		var lvgName, thinPoolName, llvName string
		if replicaType == srvv1alpha1.ReplicaTypeDiskful {
			lvgName, thinPoolName, err = lvgNameAndThinForNode(nodeName, rsp.Spec.LVMVolumeGroups, lvgs)
			if err != nil {
				return fmt.Errorf("failed to get LVMVolumeGroup for node %q: %w", nodeName, err)
			}

			// Create LLV (without ownerRef for now).
			llvName = computeLLVName(rvrName, lvgName, thinPoolName)
			if err := m.createLLV(ctx, log, rvrName, resName, lvgName, thinPoolName, size); err != nil {
				return fmt.Errorf("failed to create LLV for node %q: %w", nodeName, err)
			}
		}

		// Create DRBDR (without ownerRef for now).
		if err := m.createDRBDResource(ctx, log, resName, rvrName, nodeName, replicaType, uint8(nodeID), size, lvgName, thinPoolName); err != nil {
			return fmt.Errorf("failed to create DRBDResource for node %q: %w", nodeName, err)
		}

		replicas = append(replicas, replicaInfo{
			nodeName:     nodeName,
			nodeID:       uint8(nodeID),
			replicaType:  replicaType,
			rvrName:      rvrName,
			llvName:      llvName,
			lvgName:      lvgName,
			thinPoolName: thinPoolName,
		})
	}

	// Step 2: Create RVR.
	for _, r := range replicas {
		if _, err := m.createRVR(ctx, log, resName, r.nodeName, r.replicaType, r.lvgName, r.thinPoolName, r.nodeID); err != nil {
			return fmt.Errorf("failed to create RVR for node %q: %w", r.nodeName, err)
		}
	}

	// Step 3: Set ownerRef on RVR for LLV and DRBDR.
	for _, r := range replicas {
		if r.replicaType == srvv1alpha1.ReplicaTypeDiskful && r.llvName != "" {
			if err := m.setLLVOwnerRef(ctx, log, r.llvName, r.rvrName); err != nil {
				return fmt.Errorf("failed to set ownerRef on LLV %s: %w", r.llvName, err)
			}
		}

		if err := m.setDRBDResourceOwnerRef(ctx, log, r.rvrName, r.rvrName); err != nil {
			return fmt.Errorf("failed to set ownerRef on DRBDResource %s: %w", r.rvrName, err)
		}
	}

	// Step 4
	// Calculate FTT and GMDR based on replica types.
	diskfulReplicas, tieBreakerReplicas := 0, 0
	for _, r := range replicas {
		switch r.replicaType {
		case srvv1alpha1.ReplicaTypeDiskful:
			diskfulReplicas++
		case srvv1alpha1.ReplicaTypeTieBreaker:
			tieBreakerReplicas++
		}
	}
	ftt, gmdr := m.computeMigrationFTTGMDR(log, resName, diskfulReplicas, tieBreakerReplicas)

	// Create RV with adopt annotations (RVR + optional shared secret from LINSTOR).
	if err := m.createRV(ctx, log, resName, size, sharedSecret, pv != nil, rvCreateOptions{
		manualPoolName: rsp.Name,
		manualFTT:      ftt,
		manualGMDR:     gmdr,
	}); err != nil {
		return fmt.Errorf("failed to create RV: %w", err)
	}

	// Step 5: Create RVA if VolumeAttachment exists.
	if pv != nil {
		if err := m.createRVAFromVolumeAttachments(ctx, log, resName, vaList); err != nil {
			return fmt.Errorf("failed to create RVA: %w", err)
		}
	}

	log.Info("resource migration completed")
	return nil
}

// createRVR idempotently creates a ReplicatedVolumeReplica with deterministic naming.
func (m *Migrator) createRVR(
	ctx context.Context,
	log *slog.Logger,
	resName string,
	nodeName string,
	replicaType srvv1alpha1.ReplicaType,
	lvgName string,
	thinPoolName string,
	nodeID uint8,
) (string, error) {
	rvrName := srvv1alpha1.FormatReplicatedVolumeReplicaName(resName, nodeID)
	opLog := log.With(
		"replicatedVolumeReplica", rvrName,
		"node", nodeName,
		"type", replicaType,
		"nodeID", nodeID,
		"lvg", lvgName,
		"thinPool", thinPoolName,
	)

	rvr := &srvv1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrName,
		},
		Spec: srvv1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: resName,
			NodeName:             nodeName,
			Type:                 replicaType,
		},
	}

	// Set LVG fields for Diskful replicas.
	if replicaType == srvv1alpha1.ReplicaTypeDiskful {
		rvr.Spec.LVMVolumeGroupName = lvgName
		if thinPoolName != "" {
			rvr.Spec.LVMVolumeGroupThinPoolName = thinPoolName
		}
	}

	if err := m.retryTransient(ctx, "create ReplicatedVolumeReplica", func() error {
		return m.createIfNotExists(ctx, opLog, rvr, "ReplicatedVolumeReplica")
	}); err != nil {
		return "", err
	}
	return rvrName, nil
}

// createLLV idempotently creates an LVMLogicalVolume for a Diskful replica.
// The K8s object name is computed the same way as the controller does:
// rvrName + "-" + fnv128(lvgName + "\x00" + thinPoolName).
// ActualLVNameOnTheNode uses the LINSTOR convention (pvName + "_00000").
func (m *Migrator) createLLV(
	ctx context.Context,
	log *slog.Logger,
	rvrName string,
	resName string,
	lvgName string,
	thinPoolName string,
	size resource.Quantity,
) error {
	llvName := computeLLVName(rvrName, lvgName, thinPoolName)

	var lvmType string
	if thinPoolName != "" {
		lvmType = "Thin"
	} else {
		lvmType = "Thick"
	}

	opLog := log.With(
		"lvmLogicalVolume", llvName,
		"replicatedVolumeReplica", rvrName,
		"size", size.String(),
		"type", lvmType,
		"lvg", lvgName,
		"thinPool", thinPoolName,
	)

	llv := &sncv1alpha1.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: llvName,
		},
		Spec: sncv1alpha1.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: resName + config.LinstorLVMSuffix,
			LVMVolumeGroupName:    lvgName,
			Type:                  lvmType,
			Size:                  size.String(),
		},
	}

	if thinPoolName != "" {
		llv.Spec.Thin = &sncv1alpha1.LVMLogicalVolumeThinSpec{
			PoolName: thinPoolName,
		}
	}

	return m.retryTransient(ctx, "create LVMLogicalVolume", func() error {
		return m.createIfNotExists(ctx, opLog, llv, "LVMLogicalVolume")
	})
}

// createDRBDResource idempotently creates a DRBDResource.
func (m *Migrator) createDRBDResource(
	ctx context.Context,
	log *slog.Logger,
	resName string,
	rvrName string,
	nodeName string,
	replicaType srvv1alpha1.ReplicaType,
	nodeID uint8,
	size resource.Quantity,
	lvgName string,
	thinPoolName string,
) error {
	opLog := log.With(
		"drbdResource", rvrName,
		"node", nodeName,
		"nodeID", nodeID,
		"type", replicaType,
		"lvg", lvgName,
		"thinPool", thinPoolName,
	)

	var drbdType srvv1alpha1.DRBDResourceType
	switch replicaType {
	case srvv1alpha1.ReplicaTypeDiskful:
		drbdType = srvv1alpha1.DRBDResourceTypeDiskful
	case srvv1alpha1.ReplicaTypeTieBreaker:
		// TieBreaker is represented as diskless in DRBD.
		drbdType = srvv1alpha1.DRBDResourceTypeDiskless
	default:
		// Access and other types are diskless.
		drbdType = srvv1alpha1.DRBDResourceTypeDiskless
	}

	drbdr := &srvv1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrName,
		},
		Spec: srvv1alpha1.DRBDResourceSpec{
			ActualNameOnTheNode: resName,
			NodeName:            nodeName,
			NodeID:              nodeID,
			Type:                drbdType,
			SystemNetworks:      []string{"Internal"},
			Maintenance:         srvv1alpha1.MaintenanceModeNoResourceReconciliation,
		},
	}

	// Set size and LLV name for Diskful resources.
	if replicaType == srvv1alpha1.ReplicaTypeDiskful {
		drbdr.Spec.Size = &size
		drbdr.Spec.LVMLogicalVolumeName = computeLLVName(rvrName, lvgName, thinPoolName)
	}

	return m.retryTransient(ctx, "create DRBDResource", func() error {
		return m.createIfNotExists(ctx, opLog, drbdr, "DRBDResource")
	})
}

// createRVAFromVolumeAttachments creates a ReplicatedVolumeAttachment if there is
// a matching VolumeAttachment for the given PV.
func (m *Migrator) createRVAFromVolumeAttachments(
	ctx context.Context,
	log *slog.Logger,
	resName string,
	vaList []storagev1.VolumeAttachment,
) error {
	for _, va := range vaList {
		if va.Spec.Source.PersistentVolumeName == nil {
			continue
		}
		if !strings.EqualFold(*va.Spec.Source.PersistentVolumeName, resName) {
			continue
		}

		nodeName := strings.ToLower(va.Spec.NodeName)
		rvaName := srvv1alpha1.FormatReplicatedVolumeAttachmentName(resName, nodeName)
		rva := &srvv1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       rvaName,
				Finalizers: csiControllerFinalizers(),
			},
			Spec: srvv1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: resName,
				NodeName:             nodeName,
			},
		}

		opLog := log.With("replicatedVolumeAttachment", rvaName, "node", rva.Spec.NodeName)
		if err := m.retryTransient(ctx, "create ReplicatedVolumeAttachment", func() error {
			return m.createIfNotExists(ctx, opLog, rva, "ReplicatedVolumeAttachment")
		}); err != nil {
			return err
		}
	}

	return nil
}

// computeLLVName computes the LVMLogicalVolume K8s object name for a given RVR.
// Must stay in sync with images/controller/internal/rvr_llv_name.ComputeLLVName.
func computeLLVName(rvrName, lvgName, thinPoolName string) string {
	h := fnv.New128a()
	h.Write([]byte(lvgName))
	h.Write([]byte{0})
	h.Write([]byte(thinPoolName))
	checksum := hex.EncodeToString(h.Sum(nil))
	return rvrName + "-" + checksum
}

// resourceFlagToReplicaType converts LINSTOR resource flags to the replica type.
func resourceFlagToReplicaType(flags int) srvv1alpha1.ReplicaType {
	switch flags {
	case linstordb.ResourceFlagDiskful:
		return srvv1alpha1.ReplicaTypeDiskful
	case linstordb.ResourceFlagTieBreaker:
		return srvv1alpha1.ReplicaTypeTieBreaker
	default:
		// 260 (Diskless) and any other flags map to Access type.
		return srvv1alpha1.ReplicaTypeAccess
	}
}

// validateRSPReferencedLVMVolumeGroupsExist checks that every LVMVolumeGroup name in rsp.Spec.lvmVolumeGroups
// exists in the cluster LVMVolumeGroup snapshot.
func validateRSPReferencedLVMVolumeGroupsExist(
	rsp *srvv1alpha1.ReplicatedStoragePool,
	lvgs map[string]sncv1alpha1.LVMVolumeGroup,
	linstorPoolName string,
) error {
	for _, ent := range rsp.Spec.LVMVolumeGroups {
		if _, ok := lvgs[ent.Name]; !ok {
			return fmt.Errorf("LVMVolumeGroup %q not found for LINSTOR pool %q", ent.Name, linstorPoolName)
		}
	}
	return nil
}

// lvgNameAndThinForNode returns the LVMVolumeGroup CR name and thin pool name for a node by matching
// rsp.Spec.lvmVolumeGroups entries against cluster LVMVolumeGroup objects (Local.NodeName). Thin pool
// comes from the RSP spec entry, not from LVG defaults.
func lvgNameAndThinForNode(
	nodeName string,
	rspLVGSpecs []srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups,
	lvgs map[string]sncv1alpha1.LVMVolumeGroup,
) (lvgName, thinPoolName string, err error) {
	for _, specEnt := range rspLVGSpecs {
		lvg, ok := lvgs[specEnt.Name]
		if !ok {
			continue
		}
		if strings.EqualFold(lvg.Spec.Local.NodeName, nodeName) {
			return specEnt.Name, specEnt.ThinPoolName, nil
		}
	}
	return "", "", fmt.Errorf("LVMVolumeGroup not found for node %q", nodeName)
}

// ensureMigrationRSP ensures a ReplicatedStoragePool exists for the LINSTOR pool (name linstor-auto-<slug>).
//
// Idempotent: Create is used with AlreadyExists treated as success (createIfNotExists), so re-runs of the
// migrator do not replace an existing pool. The returned object carries the spec derived from LINSTOR CRDs
// for wiring during migration, even when the API object already existed.
func (m *Migrator) ensureMigrationRSP(
	ctx context.Context,
	linstorPoolName string,
	db *linstordb.LinstorDB,
	lvgs map[string]sncv1alpha1.LVMVolumeGroup,
) (*srvv1alpha1.ReplicatedStoragePool, error) {
	build, err := db.BuildAutoReplicatedStoragePoolSpec(linstorPoolName, lvgs)
	if err != nil {
		return nil, err
	}
	name := AutoReplicatedStoragePoolName(linstorPoolName)
	rsp := &srvv1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: srvv1alpha1.ReplicatedStoragePoolSpec{
			Type:                build.Type,
			LVMVolumeGroups:     build.LVMVolumeGroups,
			SystemNetworkNames:  []string{"Internal"},
			EligibleNodesPolicy: srvv1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute}},
		},
	}
	log := m.log.With("linstorPool", linstorPoolName, "replicatedStoragePool", name)
	if err := m.retryTransient(ctx, "create ReplicatedStoragePool", func() error {
		return m.createIfNotExists(ctx, log, rsp, "ReplicatedStoragePool")
	}); err != nil {
		return nil, err
	}
	return rsp, nil
}

// computeMigrationFTTGMDR derives failuresToTolerate and guaranteedMinimumDataRedundancy from legacy replica counts.
//
// NOTE: The new control-plane v1 Manual configuration currently caps FTT and GMDR at 1.
// Legacy volumes that include TieBreaker replicas cannot be mapped to their true layout yet
// (e.g. Availability 2D+1TB is FTT=1, GMDR=0); the default branch coerces them to (1, 1).
//
// TODO: When the RV controller gains full TieBreaker-aware layout support, replace this heuristic
// with the mapping from diskful/TieBreaker counts to (FTT, GMDR) per
// images/controller/internal/controllers/rv_controller/datamesh/README.md#legacy-replication-parameter
// (and the layout formulas in the same document: D = FTT + GMDR + 1, TB placement rules).
func (m *Migrator) computeMigrationFTTGMDR(log *slog.Logger, resName string, diskful, tieBreaker int) (ftt, gmdr byte) {
	switch {
	case diskful <= 1 && tieBreaker == 0:
		ftt, gmdr = 0, 0
	case diskful == 2 && tieBreaker == 0:
		ftt, gmdr = 1, 0
	default:
		if diskful > 3 || tieBreaker > 1 {
			log.Warn("legacy replica count exceeds v1 expressiveness; capping FTT/GMDR=(1,1)",
				"resource", resName,
				"diskfulReplicas", diskful,
				"tieBreakerReplicas", tieBreaker,
			)
		}
		ftt, gmdr = 1, 1
	}
	log.Debug("computed migration FTT/GMDR",
		"resource", resName,
		"diskfulReplicas", diskful,
		"tieBreakerReplicas", tieBreaker,
		"failuresToTolerate", ftt,
		"guaranteedMinimumDataRedundancy", gmdr,
	)
	return ftt, gmdr
}

type rvCreateOptions struct {
	manualPoolName string
	manualFTT      byte
	manualGMDR     byte
}

// createRV idempotently creates a ReplicatedVolume with adopt-rvr annotation.
//
// All migrated ReplicatedVolumes use Manual configuration intentionally.
//
// A PV may still reference a pre-migration ReplicatedStorageClass (by storage class name),
// but that RSC usually points at a legacy ReplicatedStoragePool name. During migration we
// create per-pool ReplicatedStoragePools as linstor-auto-*; stage 3 removes legacy RSP
// objects that pass the legacy candidate filter.
//
// Auto mode would derive rv.status.configuration from the RSC, pinning
// replicatedStoragePoolName to that legacy (often deleted) pool. The RV controller then
// fails to load the RSP (getRSP) and never runs adopt/v1 formation; stage 2 waits for that
// formation to finish.
//
// Manual mode sets spec.manualConfiguration.replicatedStoragePoolName to the migration RSP
// (linstor-auto-*) created for the LINSTOR pool, which is the pool adopt/v1 needs.
//
// When sharedSecret is non-empty, AdoptSharedSecretAnnotationKey is set so adopt/v1 formation reuses the LINSTOR secret.
// If hasPersistentVolume is false, LabelKeyNoPersistentVolume is set (LINSTOR resource had no PV in cluster).
func (m *Migrator) createRV(
	ctx context.Context,
	log *slog.Logger,
	resName string,
	size resource.Quantity,
	sharedSecret string,
	hasPersistentVolume bool,
	opts rvCreateOptions,
) error {
	spec := srvv1alpha1.ReplicatedVolumeSpec{
		Size:              size,
		MaxAttachments:    ptr.To(byte(2)),
		ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
		ManualConfiguration: &srvv1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: opts.manualPoolName,
			// TODO: Calculate topology instead of hard-coded value.
			Topology:                        srvv1alpha1.TopologyIgnored,
			FailuresToTolerate:              opts.manualFTT,
			GuaranteedMinimumDataRedundancy: opts.manualGMDR,
			// TODO: Calculate volumeAccess instead of hard-coded value.
			VolumeAccess: srvv1alpha1.VolumeAccessPreferablyLocal,
		},
	}

	var labels map[string]string
	if !hasPersistentVolume {
		labels = map[string]string{
			config.LabelKeyNoPersistentVolume: config.LabelValueNoPersistentVolume,
		}
	}

	ann := map[string]string{
		srvv1alpha1.AdoptRVRAnnotationKey: "",
	}
	if sharedSecret != "" {
		ann[srvv1alpha1.AdoptSharedSecretAnnotationKey] = sharedSecret
	}

	rv := &srvv1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        resName,
			Labels:      labels,
			Annotations: ann,
			Finalizers:  csiControllerFinalizers(),
		},
		Spec: spec,
	}

	rvLog := log.With("replicatedVolume", resName)
	return m.retryTransient(ctx, "create ReplicatedVolume", func() error {
		return m.createIfNotExists(ctx, rvLog, rv, "ReplicatedVolume")
	})
}

// setLLVOwnerRef sets ownerReference to RVR for LLV using controllerutil.SetControllerReference.
// The entire Get->Modify->Patch sequence is wrapped in retryTransient so that a 409 conflict on
// Patch triggers a full re-read of the object and retries the operation.
func (m *Migrator) setLLVOwnerRef(ctx context.Context, log *slog.Logger, llvName, rvrName string) error {
	return m.retryTransient(ctx, "set LLV ownerRef", func() error {
		llv := &sncv1alpha1.LVMLogicalVolume{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: llvName}, llv); err != nil {
			return fmt.Errorf("failed to get LLV: %w", err)
		}

		// Check if ownerRef already set.
		for _, ref := range llv.OwnerReferences {
			if ref.Name == rvrName {
				log.Debug("LLV already has ownerRef to RVR", "llv", llvName, "rvr", rvrName)
				return nil
			}
		}

		// Save a copy of the original LLV for the patch.
		oldLLV := llv.DeepCopy()

		// Get RVR to use as owner.
		rvr := &srvv1alpha1.ReplicatedVolumeReplica{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: rvrName}, rvr); err != nil {
			return fmt.Errorf("failed to get RVR for ownerRef: %w", err)
		}

		// Set owner reference using controllerutil.
		if err := controllerutil.SetControllerReference(rvr, llv, m.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on LLV: %w", err)
		}

		// Patch LLV using the saved copy.
		if err := m.client.Patch(ctx, llv, kubecl.MergeFrom(oldLLV)); err != nil {
			return fmt.Errorf("failed to patch LLV ownerRef: %w", err)
		}

		log.Debug("set ownerRef on LLV", "llv", llvName, "owner", rvrName)
		return nil
	})
}

// setDRBDResourceOwnerRef sets ownerReference to RVR for DRBDResource using controllerutil.SetControllerReference.
// The entire Get->Modify->Patch sequence is wrapped in retryTransient so that a 409 conflict on
// Patch triggers a full re-read of the object and retries the operation.
func (m *Migrator) setDRBDResourceOwnerRef(ctx context.Context, log *slog.Logger, drbdrName, rvrName string) error {
	return m.retryTransient(ctx, "set DRBDResource ownerRef", func() error {
		drbdr := &srvv1alpha1.DRBDResource{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: drbdrName}, drbdr); err != nil {
			return fmt.Errorf("failed to get DRBDResource: %w", err)
		}

		// Check if ownerRef already set.
		for _, ref := range drbdr.OwnerReferences {
			if ref.Name == rvrName {
				log.Debug("DRBDResource already has ownerRef to RVR", "drbdr", drbdrName, "rvr", rvrName)
				return nil
			}
		}

		// Save a copy of the original DRBDResource for the patch.
		oldDRBDR := drbdr.DeepCopy()

		// Get RVR to use as owner.
		rvr := &srvv1alpha1.ReplicatedVolumeReplica{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: rvrName}, rvr); err != nil {
			return fmt.Errorf("failed to get RVR for ownerRef: %w", err)
		}

		// Set owner reference using controllerutil.
		if err := controllerutil.SetControllerReference(rvr, drbdr, m.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on DRBDResource: %w", err)
		}

		// Patch DRBDResource using the saved copy.
		if err := m.client.Patch(ctx, drbdr, kubecl.MergeFrom(oldDRBDR)); err != nil {
			return fmt.Errorf("failed to patch DRBDResource ownerRef: %w", err)
		}

		log.Debug("set ownerRef on DRBDResource", "drbdr", drbdrName, "owner", rvrName)
		return nil
	})
}
