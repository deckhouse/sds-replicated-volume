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

	sncv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvlinstor "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstordb"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
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

	// If migration already completed, exit early.
	if currentState == config.StateAllCompleted {
		m.log.Info("migration already completed, nothing to do")
		return nil
	}

	// Collect replicated PVs.
	pvs := &corev1.PersistentVolumeList{}
	if err := m.client.List(ctx, pvs); err != nil {
		return fmt.Errorf("failed to list PersistentVolumes: %w", err)
	}
	replicatedPVs := make([]corev1.PersistentVolume, 0, len(pvs.Items))
	for _, pv := range pvs.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == config.CSIDriverReplicated {
			replicatedPVs = append(replicatedPVs, pv)
		}
	}

	if len(replicatedPVs) == 0 {
		m.log.Info("no replicated PersistentVolumes found, no migration needed")
		if err := m.ensureConfigMapState(ctx, config.StateAllCompleted); err != nil {
			return fmt.Errorf("failed to set migration state to %s: %w", config.StateAllCompleted, err)
		}
		return nil
	}
	m.log.Info("found replicated PersistentVolumes", "count", len(replicatedPVs))

	// Determine which stages to run based on current state.
	switch currentState {
	case config.StateStage2Completed:
		return m.runStage3(ctx)
	case config.StateStage1Completed:
		if err := m.runStage2(ctx); err != nil {
			return err
		}
		return m.runStage3(ctx)
	default:
		// For not_started, stage1_started, stage2_started, stage3_started — start from stage 1.
		// Idempotency guarantees safe re-runs.
		if err := m.runStage1(ctx, replicatedPVs); err != nil {
			return err
		}
		if err := m.runStage2(ctx); err != nil {
			return err
		}
		return m.runStage3(ctx)
	}
}

// runStage1 creates RV, RVR, LLV, DRBDResource, and RVA for each replicated PV.
func (m *Migrator) runStage1(ctx context.Context, replicatedPVs []corev1.PersistentVolume) error {
	m.log.Info("stage 1: starting resource migration", "pv_count", len(replicatedPVs))
	if err := m.updateMigrationState(ctx, config.StateStage1Started); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}

	// Load LINSTOR database.
	m.log.Debug("loading LINSTOR database from Kubernetes CRs")
	db, err := linstordb.Init(ctx, m.client)
	if err != nil {
		return fmt.Errorf("failed to initialize LINSTOR database: %w", err)
	}
	m.log.Info("LINSTOR database loaded")

	// Load ReplicatedStoragePools.
	rspList := &srvv1alpha1.ReplicatedStoragePoolList{}
	if err := m.client.List(ctx, rspList); err != nil {
		return fmt.Errorf("failed to list ReplicatedStoragePools: %w", err)
	}
	repStorPools := make(map[string]srvv1alpha1.ReplicatedStoragePool, len(rspList.Items))
	for _, rsp := range rspList.Items {
		repStorPools[rsp.Name] = rsp
	}
	m.log.Debug("loaded ReplicatedStoragePools", "count", len(repStorPools))

	// Load ReplicatedStorageClasses.
	rscList := &srvv1alpha1.ReplicatedStorageClassList{}
	if err := m.client.List(ctx, rscList); err != nil {
		return fmt.Errorf("failed to list ReplicatedStorageClasses: %w", err)
	}
	repStorClasses := make(map[string]srvv1alpha1.ReplicatedStorageClass, len(rscList.Items))
	for _, rsc := range rscList.Items {
		repStorClasses[rsc.Name] = rsc
	}
	m.log.Debug("loaded ReplicatedStorageClasses", "count", len(repStorClasses))

	// Load VolumeAttachments.
	vaList := &storagev1.VolumeAttachmentList{}
	if err := m.client.List(ctx, vaList); err != nil {
		return fmt.Errorf("failed to list VolumeAttachments: %w", err)
	}
	m.log.Debug("loaded VolumeAttachments", "count", len(vaList.Items))

	// Migrate each PV.
	for _, pv := range replicatedPVs {
		if err := m.migratePV(ctx, pv, db, repStorPools, repStorClasses, vaList); err != nil {
			return fmt.Errorf("failed to migrate PV %s: %w", pv.Name, err)
		}
	}

	if err := m.updateMigrationState(ctx, config.StateStage1Completed); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}
	m.log.Info("stage 1: resource migration completed")
	return nil
}

// runStage2 waits for RV readiness (stub implementation).
func (m *Migrator) runStage2(ctx context.Context) error {
	m.log.Info("stage 2: starting RV readiness check")
	if err := m.updateMigrationState(ctx, config.StateStage2Started); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}

	// Stub: wait 30 seconds.
	m.log.Info("stage 2: waiting 30 seconds for RV readiness (stub)")
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := m.updateMigrationState(ctx, config.StateStage2Completed); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}
	m.log.Info("stage 2: RV readiness check completed")
	return nil
}

// runStage3 verifies RVs against LINSTOR (stub implementation).
func (m *Migrator) runStage3(ctx context.Context) error {
	m.log.Info("stage 3: starting verification")
	if err := m.updateMigrationState(ctx, config.StateStage3Started); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}

	// Stub: wait 10 seconds.
	m.log.Info("stage 3: waiting 10 seconds for verification (stub)")
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := m.updateMigrationState(ctx, config.StateAllCompleted); err != nil {
		return fmt.Errorf("failed to update migration state: %w", err)
	}
	m.log.Info("stage 3: verification completed, migration finished")
	return nil
}

// migratePV migrates a single PV by creating RV, RVRs, LLVs, DRBDResources, and RVA.
func (m *Migrator) migratePV(
	ctx context.Context,
	pv corev1.PersistentVolume,
	db *linstordb.LinstorDB,
	repStorPools map[string]srvv1alpha1.ReplicatedStoragePool,
	repStorClasses map[string]srvv1alpha1.ReplicatedStorageClass,
	vaList *storagev1.VolumeAttachmentList,
) error {
	log := m.log.With("pv", pv.Name)
	log.Info("migrating PersistentVolume")

	// Determine volume size.
	size, err := db.GetPVSize(pv)
	if err != nil {
		return fmt.Errorf("failed to get PV size: %w", err)
	}
	log.Debug("determined PV size", "size", size.String())

	// Determine pool name.
	poolName, err := db.GetPoolName(pv.Name)
	if err != nil {
		return fmt.Errorf("failed to get pool name: %w", err)
	}
	log.Debug("determined pool name", "pool", poolName)

	// Look up ReplicatedStoragePool.
	rsp, ok := repStorPools[poolName]
	if !ok {
		return fmt.Errorf("ReplicatedStoragePool %q not found", poolName)
	}

	// Look up ReplicatedStorageClass.
	if pv.Spec.StorageClassName == "" {
		return fmt.Errorf("StorageClassName is empty for PV %s", pv.Name)
	}
	rsc, ok := repStorClasses[pv.Spec.StorageClassName]
	if !ok {
		return fmt.Errorf("ReplicatedStorageClass %q not found", pv.Spec.StorageClassName)
	}

	// Fetch LVMVolumeGroups for this pool.
	poolLVGs, err := m.getLVMVolumeGroupsFromPool(ctx, log, rsp)
	if err != nil {
		return fmt.Errorf("failed to get LVMVolumeGroups for pool %q: %w", poolName, err)
	}

	// Create ReplicatedVolume.
	if err := m.createRV(ctx, log, pv.Name, size, pv.Spec.StorageClassName); err != nil {
		return fmt.Errorf("failed to create RV: %w", err)
	}

	// Get LINSTOR resources for this PV.
	linstorResources, ok := db.Resources[strings.ToLower(pv.Name)]
	if !ok {
		return fmt.Errorf("LINSTOR resources not found for PV %s", pv.Name)
	}

	// Create RVR, LLV, and DRBDResource for each LINSTOR resource.
	for _, lr := range linstorResources {
		nodeName := strings.ToLower(lr.Spec.NodeName)
		replicaType := resourceFlagToReplicaType(lr.Spec.ResourceFlags)

		// Get DRBD node ID from LINSTOR.
		nodeID, err := db.GetNodeID(pv.Name, lr.Spec.NodeName)
		if err != nil {
			return fmt.Errorf("failed to get node ID for node %q: %w", nodeName, err)
		}
		log.Debug("LINSTOR resource info", "node", nodeName, "type", replicaType, "nodeID", nodeID)

		// Determine LVG and thin pool for Diskful replicas.
		var lvmVGName, thinPoolName string
		if replicaType == srvv1alpha1.ReplicaTypeDiskful {
			lvmVGName, thinPoolName, err = linstordb.GetLVMVolumeGroupNameAndThinPoolName(lr.Spec.NodeName, poolLVGs)
			if err != nil {
				return fmt.Errorf("failed to get LVMVolumeGroup for node %q: %w", nodeName, err)
			}
		}

		// Create RVR.
		rvrName, err := m.createRVR(ctx, log, pv.Name, nodeName, replicaType, lvmVGName, thinPoolName, uint8(nodeID))
		if err != nil {
			return fmt.Errorf("failed to create RVR for node %q: %w", nodeName, err)
		}

		// Create LLV for Diskful replicas.
		if replicaType == srvv1alpha1.ReplicaTypeDiskful {
			if err := m.createLLV(ctx, log, rvrName, pv.Name, pv.Spec.StorageClassName, lvmVGName, thinPoolName, size); err != nil {
				return fmt.Errorf("failed to create LLV for node %q: %w", nodeName, err)
			}
		}

		// Create DRBDResource.
		if err := m.createDRBDResource(ctx, log, rvrName, pv.Name, nodeName, replicaType, uint8(nodeID), size, lvmVGName, thinPoolName, rsc, db, linstorResources); err != nil {
			return fmt.Errorf("failed to create DRBDResource for node %q: %w", nodeName, err)
		}
	}

	// Create RVA based on VolumeAttachments.
	if err := m.createRVAFromVolumeAttachments(ctx, log, pv.Name, vaList); err != nil {
		return fmt.Errorf("failed to create RVA: %w", err)
	}

	log.Info("PersistentVolume migration completed")
	return nil
}

// createRV idempotently creates a ReplicatedVolume resource.
func (m *Migrator) createRV(ctx context.Context, log *slog.Logger, pvName string, size resource.Quantity, storageClassName string) error {
	rv := &srvv1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
		},
		Spec: srvv1alpha1.ReplicatedVolumeSpec{
			Size:                       size,
			ReplicatedStorageClassName: storageClassName,
			MaxAttachments:             1,
		},
	}

	return m.createIfNotExists(ctx, log, rv, "ReplicatedVolume")
}

// createRVR idempotently creates a ReplicatedVolumeReplica with deterministic naming.
// It uses the LINSTOR node ID to set the RVR name: <rvName>-<nodeID>.
func (m *Migrator) createRVR(
	ctx context.Context,
	log *slog.Logger,
	rvName string,
	nodeName string,
	replicaType srvv1alpha1.ReplicaType,
	lvmVGName string,
	thinPoolName string,
	nodeID uint8,
) (string, error) {
	rvrName := srvv1alpha1.FormatReplicatedVolumeReplicaName(rvName, nodeID)

	rvr := &srvv1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrName,
		},
		Spec: srvv1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             nodeName,
			Type:                 replicaType,
		},
	}

	// Set LVG fields for Diskful replicas.
	if replicaType == srvv1alpha1.ReplicaTypeDiskful {
		rvr.Spec.LVMVolumeGroupName = lvmVGName
		if thinPoolName != "" {
			rvr.Spec.LVMVolumeGroupThinPoolName = thinPoolName
		}
	}

	if err := m.createIfNotExists(ctx, log, rvr, "ReplicatedVolumeReplica"); err != nil {
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
	pvName string,
	storageClassName string,
	lvmVGName string,
	thinPoolName string,
	size resource.Quantity,
) error {
	llvName := computeLLVName(rvrName, lvmVGName, thinPoolName)

	var lvmType string
	if thinPoolName != "" {
		lvmType = "Thin"
	} else {
		lvmType = "Thick"
	}

	llv := &sncv1alpha1.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: llvName,
			Labels: map[string]string{
				srvv1alpha1.ReplicatedVolumeLabelKey:       pvName,
				srvv1alpha1.ReplicatedStorageClassLabelKey: storageClassName,
			},
		},
		Spec: sncv1alpha1.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: pvName + config.LinstorLVMSuffix,
			LVMVolumeGroupName:    lvmVGName,
			Type:                  lvmType,
			Size:                  size.String(),
		},
	}

	if thinPoolName != "" {
		llv.Spec.Thin = &sncv1alpha1.LVMLogicalVolumeThinSpec{
			PoolName: thinPoolName,
		}
	}

	return m.createIfNotExists(ctx, log, llv, "LVMLogicalVolume")
}

// createDRBDResource idempotently creates a DRBDResource with peers populated from LINSTOR data.
func (m *Migrator) createDRBDResource(
	ctx context.Context,
	log *slog.Logger,
	rvrName string,
	pvName string,
	nodeName string,
	replicaType srvv1alpha1.ReplicaType,
	nodeID uint8,
	size resource.Quantity,
	lvmVGName string,
	thinPoolName string,
	rsc srvv1alpha1.ReplicatedStorageClass,
	db *linstordb.LinstorDB,
	allLinstorResources []srvlinstor.Resources,
) error {
	// Build peers from LINSTOR data.
	peers, err := m.buildPeers(pvName, nodeName, db, allLinstorResources)
	if err != nil {
		return fmt.Errorf("failed to build peers: %w", err)
	}

	var drbdType srvv1alpha1.DRBDResourceType
	switch replicaType {
	case srvv1alpha1.ReplicaTypeDiskful:
		drbdType = srvv1alpha1.DRBDResourceTypeDiskful
	default:
		drbdType = srvv1alpha1.DRBDResourceTypeDiskless
	}

	drbdr := &srvv1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrName,
		},
		Spec: srvv1alpha1.DRBDResourceSpec{
			NodeName:            nodeName,
			NodeID:              nodeID,
			Type:                drbdType,
			SystemNetworks:      rsc.Spec.SystemNetworkNames,
			Maintenance:         srvv1alpha1.MaintenanceModeNoResourceReconciliation,
			Peers:               peers,
			ActualNameOnTheNode: pvName,
		},
	}

	// Set size and LLV name for Diskful resources.
	if replicaType == srvv1alpha1.ReplicaTypeDiskful {
		drbdr.Spec.Size = &size
		drbdr.Spec.LVMLogicalVolumeName = computeLLVName(rvrName, lvmVGName, thinPoolName)
	}

	return m.createIfNotExists(ctx, log, drbdr, "DRBDResource")
}

// createRVAFromVolumeAttachments creates a ReplicatedVolumeAttachment if there is
// a matching VolumeAttachment for the given PV.
func (m *Migrator) createRVAFromVolumeAttachments(
	ctx context.Context,
	log *slog.Logger,
	pvName string,
	vaList *storagev1.VolumeAttachmentList,
) error {
	for _, va := range vaList.Items {
		if va.Spec.Attacher != config.CSIDriverReplicated {
			continue
		}
		if va.Spec.Source.PersistentVolumeName == nil {
			continue
		}
		if !strings.EqualFold(*va.Spec.Source.PersistentVolumeName, pvName) {
			continue
		}

		rva := &srvv1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", pvName, strings.ToLower(va.Spec.NodeName)),
			},
			Spec: srvv1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: pvName,
				NodeName:             strings.ToLower(va.Spec.NodeName),
			},
		}

		if err := m.createIfNotExists(ctx, log, rva, "ReplicatedVolumeAttachment"); err != nil {
			return err
		}
	}

	return nil
}

// buildPeers constructs the DRBDResourcePeer slice for a given resource and node,
// containing all other nodes that host replicas of this resource.
func (m *Migrator) buildPeers(
	pvName string,
	currentNodeName string,
	db *linstordb.LinstorDB,
	allLinstorResources []srvlinstor.Resources,
) ([]srvv1alpha1.DRBDResourcePeer, error) {
	sharedSecret, err := db.GetSharedSecret(pvName)
	if err != nil {
		return nil, fmt.Errorf("failed to get shared secret: %w", err)
	}

	drbdPort, err := db.GetDRBDPort(pvName)
	if err != nil {
		return nil, fmt.Errorf("failed to get DRBD port: %w", err)
	}

	var peers []srvv1alpha1.DRBDResourcePeer
	for _, lr := range allLinstorResources {
		if strings.EqualFold(lr.Spec.NodeName, currentNodeName) {
			continue
		}

		peerNodeName := strings.ToLower(lr.Spec.NodeName)

		peerNodeID, err := db.GetNodeID(pvName, lr.Spec.NodeName)
		if err != nil {
			return nil, fmt.Errorf("failed to get node ID for peer %q: %w", peerNodeName, err)
		}

		peerNodeIPv4, err := db.GetNodeIPv4(lr.Spec.NodeName)
		if err != nil {
			return nil, fmt.Errorf("failed to get IPv4 for peer %q: %w", peerNodeName, err)
		}

		peerReplicaType := resourceFlagToReplicaType(lr.Spec.ResourceFlags)
		var peerDRBDType srvv1alpha1.DRBDResourceType
		if peerReplicaType == srvv1alpha1.ReplicaTypeDiskful {
			peerDRBDType = srvv1alpha1.DRBDResourceTypeDiskful
		} else {
			peerDRBDType = srvv1alpha1.DRBDResourceTypeDiskless
		}

		// Peer name matches DRBDResource name which matches RVR name.
		peerRVRName := srvv1alpha1.FormatReplicatedVolumeReplicaName(pvName, uint8(peerNodeID))

		peer := srvv1alpha1.DRBDResourcePeer{
			Name:            peerRVRName,
			Type:            peerDRBDType,
			NodeID:          uint8(peerNodeID),
			Protocol:        srvv1alpha1.DRBDProtocolC,
			SharedSecret:    sharedSecret,
			SharedSecretAlg: srvv1alpha1.SharedSecretAlgSHA256,
			Paths: []srvv1alpha1.DRBDResourcePath{
				{
					SystemNetworkName: "default",
					Address: srvv1alpha1.DRBDAddress{
						IPv4: peerNodeIPv4,
						Port: uint(drbdPort),
					},
				},
			},
		}

		peers = append(peers, peer)
	}

	return peers, nil
}

// getLVMVolumeGroupsFromPool fetches all LVMVolumeGroup resources referenced in the ReplicatedStoragePool.
func (m *Migrator) getLVMVolumeGroupsFromPool(
	ctx context.Context,
	log *slog.Logger,
	rsp srvv1alpha1.ReplicatedStoragePool,
) (map[string]sncv1alpha1.LVMVolumeGroup, error) {
	log.Debug("fetching LVMVolumeGroups from ReplicatedStoragePool")

	lvgs := make(map[string]sncv1alpha1.LVMVolumeGroup, len(rsp.Spec.LVMVolumeGroups))
	for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
		lvg := &sncv1alpha1.LVMVolumeGroup{}
		if err := m.client.Get(ctx, types.NamespacedName{Name: rspLVG.Name}, lvg); err != nil {
			return nil, fmt.Errorf("failed to get LVMVolumeGroup %q: %w", rspLVG.Name, err)
		}
		lvgs[rspLVG.Name] = *lvg
	}

	return lvgs, nil
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
	m.log.Info("migration state updated", "state", state)
	return nil
}

// crdExists checks if a CRD with the given name exists in the cluster.
func (m *Migrator) crdExists(ctx context.Context, crdName string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	return m.client.Get(ctx, types.NamespacedName{Name: crdName}, crd)
}

// createIfNotExists creates a Kubernetes resource if it does not already exist.
// It is idempotent: if the resource already exists, it logs and returns nil.
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
