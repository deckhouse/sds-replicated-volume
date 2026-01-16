package drbdconfig

import (
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func (r *Reconciler) RVCreateShouldBeReconciled(rv *v1alpha1.ReplicatedVolume) bool {
	if !slices.Contains(rv.Finalizers, v1alpha1.ControllerFinalizer) {
		return false
	}

	if rv.Status.DRBD == nil || rv.Status.DRBD.Config == nil {
		return false
	}
	if rv.Status.DRBD.Config.SharedSecret == "" {
		return false
	}
	if rv.Status.DRBD.Config.SharedSecretAlg == "" {
		return false
	}

	return true
}

func (r *Reconciler) RVUpdateShouldBeReconciled(
	rvOld *v1alpha1.ReplicatedVolume,
	rvNew *v1alpha1.ReplicatedVolume,
) bool {
	if !r.RVCreateShouldBeReconciled(rvNew) {
		return false
	}

	// only consider important changes
	if !equality.Semantic.DeepEqual(rvOld.Status.DRBD, rvNew.Status.DRBD) {
		return true
	}
	if !equality.Semantic.DeepEqual(rvOld.Status.Conditions, rvNew.Status.Conditions) {
		return true
	}
	if !equality.Semantic.DeepEqual(rvOld.Spec.Size, rvNew.Spec.Size) {
		return true
	}

	return false
}

func (r *Reconciler) RVRCreateShouldBeReconciled(
	rvr *v1alpha1.ReplicatedVolumeReplica,
) bool {
	if rvr.Spec.NodeName != r.nodeName {
		return false
	}

	if rvr.DeletionTimestamp != nil {
		for _, f := range rvr.Finalizers {
			if f != v1alpha1.AgentFinalizer {
				return false
			}
		}
	} else {
		if rvr.Spec.ReplicatedVolumeName == "" {
			return false
		}
		if rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
			return false
		}
		if rvr.Status.DRBD.Config.Address == nil {
			return false
		}
		if !rvr.Status.DRBD.Config.PeersInitialized {
			return false
		}
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.Status.LVMLogicalVolumeName == "" {
			return false
		}
	}

	return true
}

func (r *Reconciler) RVRUpdateShouldBeReconciled(
	rvrOld *v1alpha1.ReplicatedVolumeReplica,
	rvrNew *v1alpha1.ReplicatedVolumeReplica,
) bool {
	if !r.RVRCreateShouldBeReconciled(rvrNew) {
		return false
	}

	// only consider important changes
	if !equality.Semantic.DeepEqual(rvrOld.Spec, rvrNew.Spec) {
		return true
	}
	if !equality.Semantic.DeepEqual(rvrOld.Finalizers, rvrNew.Finalizers) {
		return true
	}
	if !equality.Semantic.DeepEqual(rvrOld.DeletionTimestamp, rvrNew.DeletionTimestamp) {
		return true
	}
	if !rvrStatusDRBDConfigEqual(rvrOld, rvrNew) {
		return true
	}
	if !rvrStatusLVMLogicalVolumeNameEqual(rvrOld, rvrNew) {
		return true
	}

	return false
}

func rvrStatusDRBDConfigEqual(rvrOld, rvrNew *v1alpha1.ReplicatedVolumeReplica) bool {
	oldConfig := getDRBDConfig(rvrOld)
	newConfig := getDRBDConfig(rvrNew)
	return equality.Semantic.DeepEqual(oldConfig, newConfig)
}

func getDRBDConfig(rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.DRBDConfig {
	if rvr.Status.DRBD == nil {
		return nil
	}
	return rvr.Status.DRBD.Config
}

func rvrStatusLVMLogicalVolumeNameEqual(rvrOld, rvrNew *v1alpha1.ReplicatedVolumeReplica) bool {
	return getLVMLogicalVolumeName(rvrOld) == getLVMLogicalVolumeName(rvrNew)
}

func getLVMLogicalVolumeName(rvr *v1alpha1.ReplicatedVolumeReplica) string {
	return rvr.Status.LVMLogicalVolumeName
}
