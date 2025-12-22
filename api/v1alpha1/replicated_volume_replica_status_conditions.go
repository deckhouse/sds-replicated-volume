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

package v1alpha1

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionDataInitialized() error {
	if err := rvr.validateStatusDRBDStatusNotNil(); err != nil {
		return nil
	}

	diskful := rvr.Spec.Type == ReplicaTypeDiskful

	if !diskful {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:               ConditionTypeDataInitialized,
				Status:             v1.ConditionFalse,
				Reason:             ReasonNotApplicableToDiskless,
				ObservedGeneration: rvr.Generation,
			},
		)
		return nil
	}

	alreadyTrue := meta.IsStatusConditionTrue(rvr.Status.Conditions, ConditionTypeDataInitialized)
	if alreadyTrue {
		return nil
	}

	devices := rvr.Status.DRBD.Status.Devices

	if len(devices) == 0 {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:    ConditionTypeDataInitialized,
				Status:  v1.ConditionUnknown,
				Reason:  ReasonDataInitializedUnknownDiskState,
				Message: "No devices reported by DRBD",
			},
		)
		return nil
	}

	becameTrue := devices[0].DiskState == DiskStateUpToDate
	if becameTrue {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:               ConditionTypeDataInitialized,
				Status:             v1.ConditionTrue,
				Reason:             ReasonDiskHasBeenSeenInUpToDateState,
				ObservedGeneration: rvr.Generation,
			},
		)
		return nil
	}

	meta.SetStatusCondition(
		&rvr.Status.Conditions,
		v1.Condition{
			Type:               ConditionTypeDataInitialized,
			Status:             v1.ConditionFalse,
			Reason:             ReasonDiskNeverWasInUpToDateState,
			ObservedGeneration: rvr.Generation,
		},
	)
	return nil
}

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionInQuorum() error {
	if err := rvr.validateStatusDRBDStatusNotNil(); err != nil {
		return nil
	}

	devices := rvr.Status.DRBD.Status.Devices

	if len(devices) == 0 {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:    ConditionTypeInQuorum,
				Status:  v1.ConditionUnknown,
				Reason:  ReasonUnknownDiskState,
				Message: "No devices reported by DRBD",
			},
		)
		return nil
	}

	newCond := v1.Condition{Type: ConditionTypeInQuorum}
	newCond.ObservedGeneration = rvr.Generation

	inQuorum := devices[0].Quorum

	oldCond := meta.FindStatusCondition(rvr.Status.Conditions, ConditionTypeInQuorum)
	if oldCond == nil || oldCond.Status == v1.ConditionUnknown {
		// initial setup - simpler message
		if inQuorum {
			newCond.Status, newCond.Reason = v1.ConditionTrue, ReasonInQuorumInQuorum
		} else {
			newCond.Status, newCond.Reason = v1.ConditionFalse, ReasonInQuorumQuorumLost
		}
	} else {
		if inQuorum && oldCond.Status != v1.ConditionTrue {
			// switch to true
			newCond.Status, newCond.Reason = v1.ConditionTrue, ReasonInQuorumInQuorum
			newCond.Message = fmt.Sprintf("Quorum achieved after being lost for %v", time.Since(oldCond.LastTransitionTime.Time))
		} else if !inQuorum && oldCond.Status != v1.ConditionFalse {
			// switch to false
			newCond.Status, newCond.Reason = v1.ConditionFalse, ReasonInQuorumQuorumLost
			newCond.Message = fmt.Sprintf("Quorum lost after being achieved for %v", time.Since(oldCond.LastTransitionTime.Time))
		} else {
			// no change - keep old values
			return nil
		}
	}

	meta.SetStatusCondition(&rvr.Status.Conditions, newCond)
	return nil
}

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionInSync() error {
	if err := rvr.validateStatusDRBDStatusNotNil(); err != nil {
		return nil
	}

	devices := rvr.Status.DRBD.Status.Devices

	if len(devices) == 0 {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:    ConditionTypeInSync,
				Status:  v1.ConditionUnknown,
				Reason:  ReasonUnknownDiskState,
				Message: "No devices reported by DRBD",
			},
		)
		return nil
	}
	device := devices[0]

	if rvr.Status.ActualType == "" {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:    ConditionTypeInSync,
				Status:  v1.ConditionUnknown,
				Reason:  ReasonInSyncReplicaNotInitialized,
				Message: "Replica's actual type is not yet initialized",
			},
		)
		return nil
	}

	diskful := rvr.Status.ActualType == ReplicaTypeDiskful

	var inSync bool
	if diskful {
		inSync = device.DiskState == DiskStateUpToDate
	} else {
		inSync = device.DiskState == DiskStateDiskless
	}

	newCond := v1.Condition{Type: ConditionTypeInSync}
	newCond.ObservedGeneration = rvr.Generation

	oldCond := meta.FindStatusCondition(rvr.Status.Conditions, ConditionTypeInSync)

	if oldCond == nil || oldCond.Status == v1.ConditionUnknown {
		// initial setup - simpler message
		if inSync {
			newCond.Status, newCond.Reason = v1.ConditionTrue, reasonForStatusTrue(diskful)
		} else {
			newCond.Status, newCond.Reason = v1.ConditionFalse, reasonForStatusFalseFromDiskState(device.DiskState)
		}
	} else {
		if inSync && oldCond.Status != v1.ConditionTrue {
			// switch to true
			newCond.Status, newCond.Reason = v1.ConditionTrue, reasonForStatusTrue(diskful)
			newCond.Message = fmt.Sprintf(
				"Became synced after being not in sync with reason %s for %v",
				oldCond.Reason,
				time.Since(oldCond.LastTransitionTime.Time),
			)
		} else if !inSync && oldCond.Status != v1.ConditionFalse {
			// switch to false
			newCond.Status, newCond.Reason = v1.ConditionFalse, reasonForStatusFalseFromDiskState(device.DiskState)
			newCond.Message = fmt.Sprintf(
				"Became unsynced after being synced for %v",
				time.Since(oldCond.LastTransitionTime.Time),
			)
		} else {
			// no change - keep old values
			return nil
		}
	}

	meta.SetStatusCondition(&rvr.Status.Conditions, newCond)
	return nil
}

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionConfigured() error {
	if err := rvr.validateStatusDRBDNotNil(); err != nil {
		return err
	}

	cond := v1.Condition{
		Type:               ConditionTypeConfigured,
		ObservedGeneration: rvr.Generation,
		Status:             v1.ConditionTrue,
		Reason:             ReasonConfigured,
		Message:            "Configuration has been successfully applied",
	}

	if rvr.Status.DRBD.Errors != nil {
		switch {
		case rvr.Status.DRBD.Errors.FileSystemOperationError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReasonFileSystemOperationFailed
			cond.Message = rvr.Status.DRBD.Errors.FileSystemOperationError.Message
		case rvr.Status.DRBD.Errors.ConfigurationCommandError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReasonConfigurationCommandFailed
			cond.Message = fmt.Sprintf(
				"Command %s exited with code %d",
				rvr.Status.DRBD.Errors.ConfigurationCommandError.Command,
				rvr.Status.DRBD.Errors.ConfigurationCommandError.ExitCode,
			)
		case rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReasonSharedSecretAlgSelectionFailed
			cond.Message = fmt.Sprintf(
				"Algorithm %s is not supported by node kernel",
				rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError.UnsupportedAlg,
			)
		case rvr.Status.DRBD.Errors.LastPrimaryError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReasonPromoteFailed
			cond.Message = fmt.Sprintf(
				"Command %s exited with code %d",
				rvr.Status.DRBD.Errors.LastPrimaryError.Command,
				rvr.Status.DRBD.Errors.LastPrimaryError.ExitCode,
			)
		case rvr.Status.DRBD.Errors.LastSecondaryError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReasonDemoteFailed
			cond.Message = fmt.Sprintf(
				"Command %s exited with code %d",
				rvr.Status.DRBD.Errors.LastSecondaryError.Command,
				rvr.Status.DRBD.Errors.LastSecondaryError.ExitCode,
			)
		}
	}

	meta.SetStatusCondition(&rvr.Status.Conditions, cond)

	return nil
}

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionPublished(shouldBePrimary bool) error {
	if rvr.Spec.Type != "Access" && rvr.Spec.Type != "Diskful" {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:   ConditionTypePublished,
				Status: v1.ConditionFalse,
				Reason: ReasonPublishingNotApplicable,
			},
		)
		return nil
	}
	if rvr.Spec.NodeName == "" || rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
		if rvr.Status == nil {
			rvr.Status = &ReplicatedVolumeReplicaStatus{}
		}

		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:   ConditionTypePublished,
				Status: v1.ConditionUnknown,
				Reason: ReasonPublishingNotInitialized,
			},
		)
		return nil
	}

	isPrimary := rvr.Status.DRBD.Status.Role == "Primary"

	cond := v1.Condition{Type: ConditionTypePublished}

	if isPrimary {
		cond.Status = v1.ConditionTrue
		cond.Reason = ReasonPublished
	} else {
		cond.Status = v1.ConditionFalse
		if shouldBePrimary {
			cond.Reason = ReasonPublishPending
		} else {
			cond.Reason = ReasonUnpublished
		}
	}

	meta.SetStatusCondition(&rvr.Status.Conditions, cond)

	return nil
}

func (rvr *ReplicatedVolumeReplica) validateStatusDRBDNotNil() error {
	if err := validateArgNotNil(rvr.Status, "rvr.status"); err != nil {
		return err
	}
	if err := validateArgNotNil(rvr.Status.DRBD, "rvr.status.drbd"); err != nil {
		return err
	}
	return nil
}

func (rvr *ReplicatedVolumeReplica) validateStatusDRBDStatusNotNil() error {
	if err := rvr.validateStatusDRBDNotNil(); err != nil {
		return err
	}
	if err := validateArgNotNil(rvr.Status.DRBD.Status, "rvr.status.drbd.status"); err != nil {
		return err
	}
	return nil
}

func reasonForStatusTrue(diskful bool) string {
	if diskful {
		return ReasonInSync
	}
	return ReasonDiskless
}

func reasonForStatusFalseFromDiskState(diskState DiskState) string {
	switch diskState {
	case DiskStateDiskless:
		return ReasonDiskLost
	case DiskStateAttaching:
		return ReasonAttaching
	case DiskStateDetaching:
		return ReasonDetaching
	case DiskStateFailed:
		return ReasonFailed
	case DiskStateNegotiating:
		return ReasonNegotiating
	case DiskStateInconsistent:
		return ReasonInconsistent
	case DiskStateOutdated:
		return ReasonOutdated
	default:
		return ReasonUnknownDiskState
	}
}

func validateArgNotNil(arg any, argName string) error {
	if arg == nil {
		return fmt.Errorf("expected '%s' to be non-nil", argName)
	}
	return nil
}
