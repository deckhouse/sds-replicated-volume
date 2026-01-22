package rsccontroller

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

const (
	storageClassProvisioner = "replicated.csi.storage.deckhouse.io"

	storageClassKind       = "StorageClass"
	storageClassAPIVersion = "storage.k8s.io/v1"

	storageClassFinalizerName = "storage.deckhouse.io/sds-replicated-volume"

	managedLabelKey   = "storage.deckhouse.io/managed-by"
	managedLabelValue = "sds-replicated-volume"

	rscStorageClassVolumeSnapshotClassAnnotationKey   = "storage.deckhouse.io/volumesnapshotclass"
	rscStorageClassVolumeSnapshotClassAnnotationValue = "sds-replicated-volume"

	storageClassPlacementCountKey           = "replicated.csi.storage.deckhouse.io/placementCount"
	storageClassAutoEvictMinReplicaCountKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/AutoEvictMinReplicaCount"
	storageClassStoragePoolKey              = "replicated.csi.storage.deckhouse.io/storagePool"

	storageClassParamReplicasOnDifferentKey = "replicated.csi.storage.deckhouse.io/replicasOnDifferent"
	storageClassParamReplicasOnSameKey      = "replicated.csi.storage.deckhouse.io/replicasOnSame"

	storageClassParamAllowRemoteVolumeAccessKey   = "replicated.csi.storage.deckhouse.io/allowRemoteVolumeAccess"
	storageClassParamAllowRemoteVolumeAccessValue = "- fromSame:\n  - topology.kubernetes.io/zone"

	replicatedStorageClassParamNameKey = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"

	storageClassParamTopologyKey = "replicated.csi.storage.deckhouse.io/topology"
	storageClassParamZonesKey    = "replicated.csi.storage.deckhouse.io/zones"

	storageClassParamFSTypeKey = "csi.storage.k8s.io/fstype"
	fsTypeExt4                 = "ext4"

	storageClassParamPlacementPolicyKey         = "replicated.csi.storage.deckhouse.io/placementPolicy"
	placementPolicyAutoPlaceTopology            = "AutoPlaceTopology"
	storageClassParamNetProtocolKey             = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/protocol"
	netProtocolC                                = "C"
	storageClassParamNetRRConflictKey           = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/rr-conflict"
	rrConflictRetryConnect                      = "retry-connect"
	storageClassParamAutoQuorumKey              = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-quorum"
	suspendIo                                   = "suspend-io"
	storageClassParamAutoAddQuorumTieBreakerKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-add-quorum-tiebreaker"

	storageClassParamOnNoQuorumKey                 = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-quorum"
	storageClassParamOnNoDataAccessibleKey         = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-data-accessible"
	storageClassParamOnSuspendedPrimaryOutdatedKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-suspended-primary-outdated"
	primaryOutdatedForceSecondary                  = "force-secondary"

	storageClassParamAutoDiskfulKey             = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-diskful"
	storageClassParamAutoDiskfulAllowCleanupKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-diskful-allow-cleanup"

	quorumMinimumRedundancyWithPrefixSCKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/quorum-minimum-redundancy"

	zoneLabel                  = "topology.kubernetes.io/zone"
	storageClassLabelKeyPrefix = "class.storage.deckhouse.io"

	storageClassVirtualizationAnnotationKey   = "virtualdisk.virtualization.deckhouse.io/access-mode"
	storageClassVirtualizationAnnotationValue = "ReadWriteOnce"
	storageClassIgnoreLocalAnnotationKey      = "replicatedstorageclass.storage.deckhouse.io/ignore-local"

	controllerConfigMapName        = "sds-replicated-volume-controller-config"
	virtualizationModuleEnabledKey = "virtualizationEnabled"

	podNamespaceEnvVar         = "POD_NAMESPACE"
	controllerNamespaceDefault = "d8-sds-replicated-volume"
)

func (r *Reconciler) reconcileStorageClass(ctx context.Context, rsc *v1alpha1.ReplicatedStorageClass) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "storageclass")
	defer rf.OnEnd(&outcome)

	oldSC, err := r.getStorageClass(rf.Ctx(), rsc.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			oldSC = nil
		} else {
			return rf.Fail(err)
		}
	}

	if oldSC != nil && oldSC.Provisioner != storageClassProvisioner {
		return rf.Fail(fmt.Errorf("reconcile StorageClass with provisioner %s is not allowed", oldSC.Provisioner))
	}

	if rsc.DeletionTimestamp != nil {
		if oldSC == nil {
			return rf.Continue()
		}
		if err := r.deleteStorageClass(rf.Ctx(), oldSC); err != nil {
			return rf.Fail(err)
		}
		return rf.Continue()
	}

	virtualizationEnabled := false
	if rsc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal {
		virtualizationEnabled, err = r.getVirtualizationModuleEnabled(rf.Ctx())
		if err != nil {
			return rf.Fail(err)
		}
	}

	newSC := computeIntendedStorageClass(rsc, virtualizationEnabled)

	if oldSC == nil {
		if err := r.createStorageClass(rf.Ctx(), newSC); err != nil {
			return rf.Fail(err)
		}
		return rf.Continue()
	}

	doUpdateStorageClass(newSC, oldSC)

	isRecreated, err := r.recreateStorageClassIfNeeded(rf.Ctx(), newSC, oldSC)
	if err != nil {
		return rf.Fail(err)
	}
	if isRecreated {
		return rf.Continue()
	}

	if err := r.updateStorageClassMetaDataIfNeeded(rf.Ctx(), newSC, oldSC); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

func (r *Reconciler) getStorageClass(ctx context.Context, name string) (*storagev1.StorageClass, error) {
	var sc storagev1.StorageClass
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

func (r *Reconciler) createStorageClass(ctx context.Context, sc *storagev1.StorageClass) error {
	return r.cl.Create(ctx, sc)
}

func (r *Reconciler) deleteStorageClass(ctx context.Context, sc *storagev1.StorageClass) error {
	finalizers := sc.Finalizers
	switch len(finalizers) {
	case 0:
		return r.cl.Delete(ctx, sc)
	case 1:
		if finalizers[0] != storageClassFinalizerName {
			return fmt.Errorf("deletion of StorageClass with finalizer %s is not allowed", finalizers[0])
		}
		sc.Finalizers = nil
		if err := r.cl.Update(ctx, sc); err != nil {
			return fmt.Errorf("error updating StorageClass to remove finalizer %s: %w", storageClassFinalizerName, err)
		}
		return r.cl.Delete(ctx, sc)
	default:
		return fmt.Errorf("deletion of StorageClass with multiple(%v) finalizers is not allowed", finalizers)
	}
}

func computeIntendedStorageClass(rsc *v1alpha1.ReplicatedStorageClass, virtualizationEnabled bool) *storagev1.StorageClass {
	allowVolumeExpansion := true
	reclaimPolicy := corev1.PersistentVolumeReclaimPolicy(rsc.Spec.ReclaimPolicy)

	params := map[string]string{
		storageClassParamFSTypeKey:                     fsTypeExt4,
		storageClassStoragePoolKey:                     rsc.Spec.StoragePool,
		storageClassParamPlacementPolicyKey:            placementPolicyAutoPlaceTopology,
		storageClassParamNetProtocolKey:                netProtocolC,
		storageClassParamNetRRConflictKey:              rrConflictRetryConnect,
		storageClassParamAutoAddQuorumTieBreakerKey:    "true",
		storageClassParamOnNoQuorumKey:                 suspendIo,
		storageClassParamOnNoDataAccessibleKey:         suspendIo,
		storageClassParamOnSuspendedPrimaryOutdatedKey: primaryOutdatedForceSecondary,
		replicatedStorageClassParamNameKey:             rsc.Name,
	}

	switch rsc.Spec.Replication {
	case v1alpha1.ReplicationNone:
		params[storageClassPlacementCountKey] = "1"
		params[storageClassAutoEvictMinReplicaCountKey] = "1"
		params[storageClassParamAutoQuorumKey] = suspendIo
	case v1alpha1.ReplicationAvailability:
		params[storageClassPlacementCountKey] = "2"
		params[storageClassAutoEvictMinReplicaCountKey] = "2"
		params[storageClassParamAutoQuorumKey] = suspendIo
	case v1alpha1.ReplicationConsistencyAndAvailability:
		params[storageClassPlacementCountKey] = "3"
		params[storageClassAutoEvictMinReplicaCountKey] = "3"
		params[storageClassParamAutoQuorumKey] = suspendIo
		params[quorumMinimumRedundancyWithPrefixSCKey] = "2"
	case v1alpha1.ReplicationConsistency:
		params[storageClassPlacementCountKey] = "2"
		params[storageClassAutoEvictMinReplicaCountKey] = "2"
		params[storageClassParamAutoQuorumKey] = suspendIo
	}

	var volumeBindingMode storagev1.VolumeBindingMode
	switch rsc.Spec.VolumeAccess {
	case v1alpha1.VolumeAccessLocal:
		params[storageClassParamAllowRemoteVolumeAccessKey] = "false"
		volumeBindingMode = storagev1.VolumeBindingWaitForFirstConsumer
	case v1alpha1.VolumeAccessEventuallyLocal:
		params[storageClassParamAutoDiskfulKey] = "30"
		params[storageClassParamAutoDiskfulAllowCleanupKey] = "true"
		params[storageClassParamAllowRemoteVolumeAccessKey] = storageClassParamAllowRemoteVolumeAccessValue
		volumeBindingMode = storagev1.VolumeBindingWaitForFirstConsumer
	case v1alpha1.VolumeAccessPreferablyLocal:
		params[storageClassParamAllowRemoteVolumeAccessKey] = storageClassParamAllowRemoteVolumeAccessValue
		volumeBindingMode = storagev1.VolumeBindingWaitForFirstConsumer
	case v1alpha1.VolumeAccessAny:
		params[storageClassParamAllowRemoteVolumeAccessKey] = storageClassParamAllowRemoteVolumeAccessValue
		volumeBindingMode = storagev1.VolumeBindingImmediate
	}

	params[storageClassParamTopologyKey] = string(rsc.Spec.Topology)
	if len(rsc.Spec.Zones) > 0 {
		var b strings.Builder
		for i, zone := range rsc.Spec.Zones {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("- ")
			b.WriteString(zone)
		}
		params[storageClassParamZonesKey] = b.String()
	}

	switch rsc.Spec.Topology {
	case v1alpha1.RSCTopologyTransZonal:
		params[storageClassParamReplicasOnSameKey] = fmt.Sprintf("%s/%s", storageClassLabelKeyPrefix, rsc.Name)
		params[storageClassParamReplicasOnDifferentKey] = zoneLabel
	case v1alpha1.RSCTopologyZonal:
		params[storageClassParamReplicasOnSameKey] = zoneLabel
		params[storageClassParamReplicasOnDifferentKey] = corev1.LabelHostname
	case v1alpha1.RSCTopologyIgnored:
		params[storageClassParamReplicasOnDifferentKey] = corev1.LabelHostname
	}

	annotations := map[string]string{
		rscStorageClassVolumeSnapshotClassAnnotationKey: rscStorageClassVolumeSnapshotClassAnnotationValue,
	}

	if rsc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal && virtualizationEnabled {
		ignoreLocal, _ := strconv.ParseBool(rsc.Annotations[storageClassIgnoreLocalAnnotationKey])
		if !ignoreLocal {
			annotations[storageClassVirtualizationAnnotationKey] = storageClassVirtualizationAnnotationValue
		}
	}

	return &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       storageClassKind,
			APIVersion: storageClassAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        rsc.Name,
			Namespace:   rsc.Namespace,
			Finalizers:  []string{storageClassFinalizerName},
			Labels:      map[string]string{managedLabelKey: managedLabelValue},
			Annotations: annotations,
		},
		AllowVolumeExpansion: &allowVolumeExpansion,
		Parameters:           params,
		Provisioner:          storageClassProvisioner,
		ReclaimPolicy:        &reclaimPolicy,
		VolumeBindingMode:    &volumeBindingMode,
	}
}

func compareStorageClasses(newSC, oldSC *storagev1.StorageClass) (bool, string) {
	var b strings.Builder
	equal := true
	b.WriteString("Old StorageClass and New StorageClass are not equal: ")

	if !maps.Equal(oldSC.Parameters, newSC.Parameters) {
		equal = false
		b.WriteString(fmt.Sprintf("Parameters are not equal (ReplicatedStorageClass parameters: %+v, StorageClass parameters: %+v); ", newSC.Parameters, oldSC.Parameters))
	}
	if oldSC.Provisioner != newSC.Provisioner {
		equal = false
		b.WriteString(fmt.Sprintf("Provisioner are not equal (Old StorageClass: %s, New StorageClass: %s); ", oldSC.Provisioner, newSC.Provisioner))
	}
	if oldSC.ReclaimPolicy != nil && newSC.ReclaimPolicy != nil && *oldSC.ReclaimPolicy != *newSC.ReclaimPolicy {
		equal = false
		b.WriteString(fmt.Sprintf("ReclaimPolicy are not equal (Old StorageClass: %s, New StorageClass: %s", string(*oldSC.ReclaimPolicy), string(*newSC.ReclaimPolicy)))
	}
	if oldSC.VolumeBindingMode != nil && newSC.VolumeBindingMode != nil && *oldSC.VolumeBindingMode != *newSC.VolumeBindingMode {
		equal = false
		b.WriteString(fmt.Sprintf("VolumeBindingMode are not equal (Old StorageClass: %s, New StorageClass: %s); ", string(*oldSC.VolumeBindingMode), string(*newSC.VolumeBindingMode)))
	}

	return equal, b.String()
}

func canRecreateStorageClass(newSC, oldSC *storagev1.StorageClass) (bool, string) {
	newSCCopy := newSC.DeepCopy()
	oldSCCopy := oldSC.DeepCopy()

	delete(newSCCopy.Parameters, quorumMinimumRedundancyWithPrefixSCKey)
	delete(newSCCopy.Parameters, replicatedStorageClassParamNameKey)
	delete(newSCCopy.Parameters, storageClassParamTopologyKey)
	delete(newSCCopy.Parameters, storageClassParamZonesKey)

	delete(oldSCCopy.Parameters, quorumMinimumRedundancyWithPrefixSCKey)
	delete(oldSCCopy.Parameters, replicatedStorageClassParamNameKey)
	delete(oldSCCopy.Parameters, storageClassParamTopologyKey)
	delete(oldSCCopy.Parameters, storageClassParamZonesKey)

	return compareStorageClasses(newSCCopy, oldSCCopy)
}

func (r *Reconciler) recreateStorageClassIfNeeded(ctx context.Context, newSC, oldSC *storagev1.StorageClass) (bool, error) {
	equal, _ := compareStorageClasses(newSC, oldSC)
	if equal {
		return false, nil
	}

	canRecreate, msg := canRecreateStorageClass(newSC, oldSC)
	if !canRecreate {
		return false, fmt.Errorf("the StorageClass cannot be recreated because its parameters are not equal: %s", msg)
	}

	if err := r.deleteStorageClass(ctx, oldSC); err != nil {
		return false, err
	}
	if err := r.createStorageClass(ctx, newSC); err != nil {
		return false, err
	}
	return true, nil
}

func areSlicesEqualIgnoreOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, item := range a {
		set[item] = struct{}{}
	}
	for _, item := range b {
		if _, found := set[item]; !found {
			return false
		}
	}
	return true
}

func (r *Reconciler) updateStorageClassMetaDataIfNeeded(ctx context.Context, newSC, oldSC *storagev1.StorageClass) error {
	needsUpdate := !maps.Equal(oldSC.Labels, newSC.Labels) ||
		!maps.Equal(oldSC.Annotations, newSC.Annotations) ||
		!areSlicesEqualIgnoreOrder(newSC.Finalizers, oldSC.Finalizers)
	if !needsUpdate {
		return nil
	}

	oldSC.Labels = maps.Clone(newSC.Labels)
	oldSC.Annotations = maps.Clone(newSC.Annotations)
	oldSC.Finalizers = slices.Clone(newSC.Finalizers)

	return r.cl.Update(ctx, oldSC)
}

func doUpdateStorageClass(newSC *storagev1.StorageClass, oldSC *storagev1.StorageClass) {
	if len(oldSC.Labels) > 0 {
		if newSC.Labels == nil {
			newSC.Labels = maps.Clone(oldSC.Labels)
		} else {
			updateMap(newSC.Labels, oldSC.Labels)
		}
	}

	copyAnnotations := maps.Clone(oldSC.Annotations)
	delete(copyAnnotations, storageClassVirtualizationAnnotationKey)

	if len(copyAnnotations) > 0 {
		if newSC.Annotations == nil {
			newSC.Annotations = copyAnnotations
		} else {
			updateMap(newSC.Annotations, copyAnnotations)
		}
	}

	if len(oldSC.Finalizers) > 0 {
		finalizersSet := make(map[string]struct{}, len(newSC.Finalizers))
		for _, f := range newSC.Finalizers {
			finalizersSet[f] = struct{}{}
		}
		for _, f := range oldSC.Finalizers {
			if _, exists := finalizersSet[f]; !exists {
				newSC.Finalizers = append(newSC.Finalizers, f)
				finalizersSet[f] = struct{}{}
			}
		}
	}
}

func updateMap(dst, src map[string]string) {
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
}

func (r *Reconciler) getVirtualizationModuleEnabled(ctx context.Context) (bool, error) {
	ns := controllerNamespace()
	var cm corev1.ConfigMap
	if err := r.cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: controllerConfigMapName}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	value, exists := cm.Data[virtualizationModuleEnabledKey]
	if !exists {
		return false, nil
	}
	return value == "true", nil
}

func controllerNamespace() string {
	if ns := os.Getenv(podNamespaceEnvVar); ns != "" {
		return ns
	}
	return controllerNamespaceDefault
}
