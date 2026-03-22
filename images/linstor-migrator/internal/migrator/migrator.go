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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstordb"
)

// Migrator performs the migration from LINSTOR CRs to the new control-plane CRs.
type Migrator struct {
	client kubecl.Client
	log    *slog.Logger
}

// New creates a new Migrator instance.
func New(client kubecl.Client, log *slog.Logger) *Migrator {
	return &Migrator{
		client: client,
		log:    log,
	}
}

// Run executes the full migration workflow: pre-flight checks, then stages 1-3.
func (m *Migrator) Run(ctx context.Context) error {
	// Pre-flight: check that new control-plane is enabled in ModuleConfig.
	if err := m.checkNewControlPlaneEnabled(ctx); err != nil {
		return err
	}

	// Pre-flight: check new control-plane CRDs exist.
	if err := m.crdExists(ctx, config.NewControlPlaneCRDName); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("new control-plane CRD %q not found in cluster; ensure the new control-plane is installed before running migration", config.NewControlPlaneCRDName)
		}
		return fmt.Errorf("failed to check new control-plane CRD %q: %w", config.NewControlPlaneCRDName, err)
	}
	m.log.Debug("new control-plane CRD found", "crd", config.NewControlPlaneCRDName)

	// Pre-flight: check LINSTOR CRDs exist.
	if err := m.crdExists(ctx, config.LinstorCRDName); err != nil {
		if apierrors.IsNotFound(err) {
			m.log.Info("LINSTOR not found in cluster, no migration needed")
			if err := m.ensureConfigMapState(ctx, config.StateAllCompleted); err != nil {
				return fmt.Errorf("failed to set migration state to %s: %w", config.StateAllCompleted, err)
			}
			return nil
		}
		return fmt.Errorf("failed to check LINSTOR CRD %q: %w", config.LinstorCRDName, err)
	}
	m.log.Debug("LINSTOR CRD found", "crd", config.LinstorCRDName)

	// Pre-flight: ensure ConfigMap exists.
	currentState, err := m.ensureConfigMap(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure migration ConfigMap: %w", err)
	}

	// Determine which stages to run based on current state.
	switch currentState {
	case config.StateAllCompleted:
		m.log.Info("migration already completed, nothing to do")
		return nil
	case config.StateStage2Completed:
		return m.runStage3(ctx)
	default:
		// For all other states (not_started, stage1_started, stage1_completed, stage2_started, stage3_started)
		// quickly skip stage 1, then run stage 2, then stage 3.
		if err := m.skipStage1(ctx); err != nil {
			return err
		}
		if err := m.runStage2(ctx); err != nil {
			return err
		}
		return m.runStage3(ctx)
	}
}

// skipStage1 quickly skips stage 1 for compatibility with existing helm templates.
// Stage 1 previously performed resource migration, but this logic has been moved
// to stage 2. This stub is required to maintain compatibility with existing
// helm templates that expect the stage1_completed state in the ConfigMap.
func (m *Migrator) skipStage1(ctx context.Context) error {
	m.log.Info("stage 1: skipping (compatibility stub)")

	if err := m.updateMigrationState(ctx, config.StateStage1Completed); err != nil {
		return fmt.Errorf("failed to set stage1_completed state: %w", err)
	}

	m.log.Info("stage 1: marked as completed (stub)")
	return nil
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
				return fmt.Errorf("failed to check linstor-controller deployment: %w", err)
			}

			m.log.Debug("linstor-controller deployment still exists, waiting...")
		}
	}
}

// runStage2 performs the actual resource migration from LINSTOR to new control-plane.
// It waits for linstor-controller removal, loads LINSTOR data, classifies resources,
// and migrates them in the correct order.
func (m *Migrator) runStage2(ctx context.Context) error {
	m.log.Info("stage 2: starting resource migration")

	if err := m.updateMigrationState(ctx, config.StateStage2Started); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}

	// 1. Wait for linstor-controller removal
	if err := m.waitForLinstorControllerRemoval(ctx); err != nil {
		return err
	}

	// 2. Load LINSTOR database
	db, err := linstordb.Init(ctx, m.client)
	if err != nil {
		return fmt.Errorf("failed to initialize LINSTOR database: %w", err)
	}
	m.log.Info("LINSTOR database loaded", "resources", len(db.Resources), "resourceDefinitions", len(db.ResourceDefinitions), "resourceGroups", len(db.ResourceGroups))

	// 3. Load PVs for analysis
	pvs := &corev1.PersistentVolumeList{}
	if err := m.client.List(ctx, pvs); err != nil {
		return fmt.Errorf("failed to list PersistentVolumes: %w", err)
	}

	// Create PV map by resource name (lowercase), filtering only PVs with replicated CSI driver.
	pvMap := make(map[string]corev1.PersistentVolume)
	for _, pv := range pvs.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == config.CSIDriverReplicated {
			pvMap[strings.ToLower(pv.Name)] = pv
		}
	}

	// 4. Classify resources
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

	// 5. Log classification results
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

	// 6. List RSCs, VolumeAttachments, LVMVolumeGroups; ensure migration RSP per LINSTOR pool.
	rscList := &srvv1alpha1.ReplicatedStorageClassList{}
	if err := m.client.List(ctx, rscList); err != nil {
		return fmt.Errorf("failed to list ReplicatedStorageClasses: %w", err)
	}
	repStorClasses := make(map[string]srvv1alpha1.ReplicatedStorageClass, len(rscList.Items))
	for _, rsc := range rscList.Items {
		repStorClasses[rsc.Name] = rsc
	}
	m.log.Debug("loaded ReplicatedStorageClasses", "count", len(repStorClasses))

	vaList := &storagev1.VolumeAttachmentList{}
	if err := m.client.List(ctx, vaList); err != nil {
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
	if err := m.client.List(ctx, lvgList); err != nil {
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

	// 7. Migrate: first resources with PV, then without PV
	for _, resName := range resourcesWithPV {
		pv := pvMap[resName]
		if err := m.migrateResource(ctx, resName, &pv, db, migrationRSPByPool, repStorClasses, replicatedVAList, lvgs); err != nil {
			return fmt.Errorf("failed to migrate resource %s (with PV): %w", resName, err)
		}
	}

	for _, resName := range resourcesWithoutPV {
		if err := m.migrateResource(ctx, resName, nil, db, migrationRSPByPool, repStorClasses, replicatedVAList, lvgs); err != nil {
			return fmt.Errorf("failed to migrate resource %s (without PV): %w", resName, err)
		}
	}

	if err := m.updateMigrationState(ctx, config.StateStage2Completed); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}

	m.log.Info("stage 2: resource migration completed")
	return nil
}

// runStage3 is a stub implementation.
func (m *Migrator) runStage3(ctx context.Context) error {
	m.log.Info("stage 3: starting (stub)")
	if err := m.updateMigrationState(ctx, config.StateStage3Started); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}

	// Stub: wait 10 seconds.
	m.log.Info("stage 3: waiting 10 seconds (stub)")
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := m.updateMigrationState(ctx, config.StateAllCompleted); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}
	m.log.Info("stage 3: completed (stub)")
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
	repStorClasses map[string]srvv1alpha1.ReplicatedStorageClass,
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
		return fmt.Errorf("failed to get size: %w", err)
	}

	rscName, rscOK := m.findReplicatedStorageClassForResource(pv, poolName, repStorClasses)

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
		"replicatedStorageClass", rscName,
		"replicatedStorageClassFound", rscOK,
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
		autoRSCName:    rscName,
		useAutoConfig:  rscOK,
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

	if err := m.createIfNotExists(ctx, opLog, rvr, "ReplicatedVolumeReplica"); err != nil {
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

	return m.createIfNotExists(ctx, opLog, llv, "LVMLogicalVolume")
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

	return m.createIfNotExists(ctx, opLog, drbdr, "DRBDResource")
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

		rvaName := fmt.Sprintf("%s-%s", resName, strings.ToLower(va.Spec.NodeName))
		rva := &srvv1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name: rvaName,
			},
			Spec: srvv1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: resName,
				NodeName:             strings.ToLower(va.Spec.NodeName),
			},
		}

		opLog := log.With("replicatedVolumeAttachment", rvaName, "node", rva.Spec.NodeName)
		if err := m.createIfNotExists(ctx, opLog, rva, "ReplicatedVolumeAttachment"); err != nil {
			return err
		}
	}

	return nil
}

// ensureConfigMap creates the migration ConfigMap if it does not exist and returns the current state.
func (m *Migrator) ensureConfigMap(ctx context.Context) (string, error) {
	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: config.ModuleNamespace,
		Name:      config.MigrationConfigMapName,
	}, cm)

	if err == nil {
		// ConfigMap exists, return current state.
		state := cm.Data["state"]
		if state == "" {
			state = config.StateNotStarted
		}
		m.log.Debug("migration ConfigMap exists", "state", state)
		return state, nil
	}

	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to get ConfigMap %s/%s: %w", config.ModuleNamespace, config.MigrationConfigMapName, err)
	}

	// ConfigMap does not exist, create it.
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.MigrationConfigMapName,
			Namespace: config.ModuleNamespace,
		},
		Data: map[string]string{
			"state": config.StateNotStarted,
		},
	}
	if err := m.client.Create(ctx, cm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Race condition: another instance created it.
			m.log.Debug("migration ConfigMap was created by another instance")
			return config.StateNotStarted, nil
		}
		return "", fmt.Errorf("failed to create ConfigMap %s/%s: %w", config.ModuleNamespace, config.MigrationConfigMapName, err)
	}

	m.log.Info("created migration ConfigMap", "namespace", config.ModuleNamespace, "name", config.MigrationConfigMapName)
	return config.StateNotStarted, nil
}

// ensureConfigMapState creates or updates the migration ConfigMap to the given state.
func (m *Migrator) ensureConfigMapState(ctx context.Context, state string) error {
	cm := &corev1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: config.ModuleNamespace,
		Name:      config.MigrationConfigMapName,
	}, cm)

	if apierrors.IsNotFound(err) {
		// Create with desired state.
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.MigrationConfigMapName,
				Namespace: config.ModuleNamespace,
			},
			Data: map[string]string{
				"state": state,
			},
		}
		if err := m.client.Create(ctx, cm); err != nil {
			return fmt.Errorf("failed to create ConfigMap: %w", err)
		}
		m.log.Info("created migration ConfigMap with state", "state", state)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Update existing ConfigMap.
	return m.patchConfigMapState(ctx, cm, state)
}

// updateMigrationState patches the migration ConfigMap with the new state.
func (m *Migrator) updateMigrationState(ctx context.Context, state string) error {
	cm := &corev1.ConfigMap{}
	if err := m.client.Get(ctx, types.NamespacedName{
		Namespace: config.ModuleNamespace,
		Name:      config.MigrationConfigMapName,
	}, cm); err != nil {
		return fmt.Errorf("failed to get ConfigMap for state update: %w", err)
	}

	return m.patchConfigMapState(ctx, cm, state)
}

// patchConfigMapState applies a merge patch to update the state field.
func (m *Migrator) patchConfigMapState(ctx context.Context, cm *corev1.ConfigMap, state string) error {
	oldCM := cm.DeepCopy()
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["state"] = state
	if err := m.client.Patch(ctx, cm, kubecl.MergeFrom(oldCM)); err != nil {
		return fmt.Errorf("failed to patch ConfigMap state to %q: %w", state, err)
	}
	m.log.Info("migration state updated in the ConfigMap", "state", state)
	return nil
}

// crdExists checks if a CRD with the given name exists in the cluster.
func (m *Migrator) crdExists(ctx context.Context, crdName string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	return m.client.Get(ctx, types.NamespacedName{Name: crdName}, crd)
}

// checkNewControlPlaneEnabled verifies that newControlPlane is set to true in ModuleConfig.
func (m *Migrator) checkNewControlPlaneEnabled(ctx context.Context) error {
	mc := &d8commonapi.ModuleConfig{}
	if err := m.client.Get(ctx, kubecl.ObjectKey{Name: config.ModuleName}, mc); err != nil {
		return fmt.Errorf("failed to get ModuleConfig %q: %w", config.ModuleName, err)
	}

	if value, exists := mc.Spec.Settings["newControlPlane"]; exists && value == true {
		m.log.Debug("new control-plane is enabled in ModuleConfig")
		return nil
	}

	return fmt.Errorf("ModuleConfig %q has newControlPlane=false; enable the new control-plane by setting spec.settings.newControlPlane to true before running migration", config.ModuleName)
}

// createIfNotExists creates a Kubernetes resource if it does not already exist.
// It is idempotent: if the resource already exists, it logs and returns nil.
// Pass a logger from log.With(...) so Info/Debug lines include stable identifying attributes (see ensureMigrationRSP).
func (m *Migrator) createIfNotExists(ctx context.Context, log *slog.Logger, obj kubecl.Object, kind string) error {
	if err := m.client.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Debug("resource already exists, skipping", "kind", kind, "name", obj.GetName())
			return nil
		}
		return fmt.Errorf("failed to create %s %q: %w", kind, obj.GetName(), err)
	}
	log.Info("created resource", "kind", kind, "name", obj.GetName())
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
	if err := m.createIfNotExists(ctx, log, rsp, "ReplicatedStoragePool"); err != nil {
		return nil, err
	}
	return rsp, nil
}

// findReplicatedStorageClassForResource returns an existing ReplicatedStorageClass name when RV
// should use Auto configuration (PV storage class name matches an RSC, or legacy pool match without PV).
func (m *Migrator) findReplicatedStorageClassForResource(
	pv *corev1.PersistentVolume,
	linstorPoolName string,
	repStorClasses map[string]srvv1alpha1.ReplicatedStorageClass,
) (rscName string, ok bool) {
	if pv != nil && pv.Spec.StorageClassName != "" {
		if _, exists := repStorClasses[pv.Spec.StorageClassName]; exists {
			return pv.Spec.StorageClassName, true
		}
		return "", false
	}
	for _, rsc := range repStorClasses {
		// nolint:staticcheck // deprecated spec.storagePool still used for legacy pool binding
		if strings.EqualFold(rsc.Spec.StoragePool, linstorPoolName) {
			return rsc.Name, true
		}
	}
	return "", false
}

// computeMigrationFTTGMDR derives failuresToTolerate and guaranteedMinimumDataRedundancy from replica counts,
// caps both at 1, and maps (0,1) Consistency to (1,0) Availability (not representable from legacy LINSTOR).
func (m *Migrator) computeMigrationFTTGMDR(log *slog.Logger, resName string, diskful, tieBreaker int) (ftt, gmdr byte) {
	var pf, pg int
	switch {
	case diskful <= 1 && tieBreaker == 0:
		pf, pg = 0, 0
	case diskful == 2 && tieBreaker == 0:
		pf, pg = 1, 0
	default:
		pf, pg = 1, 1
	}
	if pf > 1 {
		pf = 1
	}
	if pg > 1 {
		pg = 1
	}
	ftt, gmdr = byte(pf), byte(pg)
	if ftt == 0 && gmdr == 1 {
		// TODO: Revisit heuristics when (FTT=0,GMDR=1) appears; LINSTOR legacy replication did not expose Consistency-style policy.
		log.Warn("normalized inconsistent legacy FTT/GMDR: mapping (0,1) Consistency to (1,0) Availability",
			"resource", resName, "diskfulReplicas", diskful, "tieBreakerReplicas", tieBreaker)
		ftt, gmdr = 1, 0
	}
	if ftt > gmdr+1 {
		ftt = gmdr + 1
	}
	if gmdr > ftt+1 {
		gmdr = ftt + 1
	}
	return ftt, gmdr
}

type rvCreateOptions struct {
	autoRSCName    string
	useAutoConfig  bool
	manualPoolName string
	manualFTT      byte
	manualGMDR     byte
}

// createRV idempotently creates a ReplicatedVolume with adopt-rvr annotation.
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
		Size:           size,
		MaxAttachments: ptr.To(byte(2)),
	}
	if opts.useAutoConfig && opts.autoRSCName != "" {
		spec.ConfigurationMode = srvv1alpha1.ReplicatedVolumeConfigurationModeAuto
		spec.ReplicatedStorageClassName = opts.autoRSCName
	} else {
		spec.ConfigurationMode = srvv1alpha1.ReplicatedVolumeConfigurationModeManual
		spec.ManualConfiguration = &srvv1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: opts.manualPoolName,
			// TODO: Calculate topology instead of hard-coded value.
			Topology:                        srvv1alpha1.TopologyIgnored,
			FailuresToTolerate:              opts.manualFTT,
			GuaranteedMinimumDataRedundancy: opts.manualGMDR,
			// TODO: Calculate volumeAccess instead of hard-coded value.
			VolumeAccess: srvv1alpha1.VolumeAccessPreferablyLocal,
		}
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
		},
		Spec: spec,
	}

	rvLog := log.With("replicatedVolume", resName)
	return m.createIfNotExists(ctx, rvLog, rv, "ReplicatedVolume")
}

// setLLVOwnerRef sets ownerReference to RVR for LLV using controllerutil.SetControllerReference.
func (m *Migrator) setLLVOwnerRef(ctx context.Context, log *slog.Logger, llvName, rvrName string) error {
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
}

// setDRBDResourceOwnerRef sets ownerReference to RVR for DRBDResource using controllerutil.SetControllerReference.
func (m *Migrator) setDRBDResourceOwnerRef(ctx context.Context, log *slog.Logger, drbdrName, rvrName string) error {
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
}
