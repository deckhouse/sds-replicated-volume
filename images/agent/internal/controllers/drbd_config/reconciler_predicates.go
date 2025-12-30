package drbdconfig

import (
	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func (r *Reconciler) RVCreateShouldBeReconciled(rv *v1alpha1.ReplicatedVolume) bool {
	if !v1alpha1.HasControllerFinalizer(rv) {
		return false
	}

	if rv.Status == nil || rv.Status.DRBD == nil || rv.Status.DRBD.Config == nil {
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
			if f != v1alpha1.AgentAppFinalizer {
				return false
			}
		}
	} else {
		if rvr.Spec.ReplicatedVolumeName == "" {
			return false
		}
		if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
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

	// ignore unimportant changes
	rvrOldCopy := rvrOld.DeepCopy()
	rvrNewCopy := rvrNew.DeepCopy()
	cleanIgnoredFields(rvrOldCopy)
	cleanIgnoredFields(rvrNewCopy)

	return !equality.Semantic.DeepEqual(rvrOldCopy, rvrNewCopy)
}

func cleanIgnoredFields(rvrCopy *v1alpha1.ReplicatedVolumeReplica) {
	// because it is incremented on each update
	rvrCopy.ResourceVersion = ""
	if rvrCopy.Status != nil {
		if rvrCopy.Status.DRBD != nil {
			// because drbd-config updates it itself
			rvrCopy.Status.DRBD.Actual = nil
			// because scanner updates should not be important to drbd-config
			rvrCopy.Status.DRBD.Status = nil
			// because drbd-config is not responsible
			// for error handling, except own errors, which will be retried anyways
			rvrCopy.Status.DRBD.Errors = nil
			// rvrCopy.Status.DRBD.Config is important, don't add!
		}
		// because those updates are for external clients and should not be important to drbd-config
		rvrCopy.Status.Conditions = nil
		rvrCopy.Status.SyncProgress = ""
	}
}
