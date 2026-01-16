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

package v1alpha1

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (rvr *ReplicatedVolumeReplica) NodeID() (uint, bool) {
	idx := strings.LastIndex(rvr.Name, "-")
	if idx < 0 {
		return 0, false
	}

	id, err := strconv.ParseUint(rvr.Name[idx+1:], 10, 0)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

func (rvr *ReplicatedVolumeReplica) SetNameWithNodeID(nodeID uint) {
	rvr.Name = fmt.Sprintf("%s-%d", rvr.Spec.ReplicatedVolumeName, nodeID)
}

func (rvr *ReplicatedVolumeReplica) ChooseNewName(otherRVRs []ReplicatedVolumeReplica) bool {
	reservedNodeIDs := make([]uint, 0, RVRMaxNodeID)

	for i := range otherRVRs {
		otherRVR := &otherRVRs[i]
		if otherRVR.Spec.ReplicatedVolumeName != rvr.Spec.ReplicatedVolumeName {
			continue
		}

		id, ok := otherRVR.NodeID()
		if !ok {
			continue
		}
		reservedNodeIDs = append(reservedNodeIDs, id)
	}

	for i := RVRMinNodeID; i <= RVRMaxNodeID; i++ {
		if !slices.Contains(reservedNodeIDs, i) {
			rvr.SetNameWithNodeID(i)
			return true
		}
	}

	return false
}

// SetReplicatedVolume sets the ReplicatedVolumeName in Spec and ControllerReference for the RVR.
func (rvr *ReplicatedVolumeReplica) SetReplicatedVolume(rv *ReplicatedVolume, scheme *runtime.Scheme) error {
	rvr.Spec.ReplicatedVolumeName = rv.Name
	return controllerutil.SetControllerReference(rv, rvr, scheme)
}

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionDataInitialized() error {
	if err := rvr.validateStatusDRBDStatusNotNil(); err != nil {
		return nil
	}

	diskful := rvr.Spec.Type == ReplicaTypeDiskful

	if !diskful {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:               ReplicatedVolumeReplicaCondDataInitializedType,
				Status:             v1.ConditionFalse,
				Reason:             ReplicatedVolumeReplicaCondDataInitializedReasonNotApplicableToDiskless,
				ObservedGeneration: rvr.Generation,
			},
		)
		return nil
	}

	alreadyTrue := meta.IsStatusConditionTrue(rvr.Status.Conditions, ReplicatedVolumeReplicaCondDataInitializedType)
	if alreadyTrue {
		return nil
	}

	devices := rvr.Status.DRBD.Status.Devices

	if len(devices) == 0 {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:    ReplicatedVolumeReplicaCondDataInitializedType,
				Status:  v1.ConditionUnknown,
				Reason:  ReplicatedVolumeReplicaCondDataInitializedReasonUnknownDiskState,
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
				Type:               ReplicatedVolumeReplicaCondDataInitializedType,
				Status:             v1.ConditionTrue,
				Reason:             ReplicatedVolumeReplicaCondDataInitializedReasonDiskHasBeenSeenInUpToDateState,
				ObservedGeneration: rvr.Generation,
			},
		)
		return nil
	}

	meta.SetStatusCondition(
		&rvr.Status.Conditions,
		v1.Condition{
			Type:               ReplicatedVolumeReplicaCondDataInitializedType,
			Status:             v1.ConditionFalse,
			Reason:             ReplicatedVolumeReplicaCondDataInitializedReasonDiskNeverWasInUpToDateState,
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
				Type:    ReplicatedVolumeReplicaCondInQuorumType,
				Status:  v1.ConditionUnknown,
				Reason:  ReplicatedVolumeReplicaCondInQuorumReasonUnknownDiskState,
				Message: "No devices reported by DRBD",
			},
		)
		return nil
	}

	newCond := v1.Condition{Type: ReplicatedVolumeReplicaCondInQuorumType}
	newCond.ObservedGeneration = rvr.Generation

	inQuorum := devices[0].Quorum

	oldCond := meta.FindStatusCondition(rvr.Status.Conditions, ReplicatedVolumeReplicaCondInQuorumType)
	if oldCond == nil || oldCond.Status == v1.ConditionUnknown {
		// initial setup - simpler message
		if inQuorum {
			newCond.Status, newCond.Reason = v1.ConditionTrue, ReplicatedVolumeReplicaCondInQuorumReasonInQuorum
		} else {
			newCond.Status, newCond.Reason = v1.ConditionFalse, ReplicatedVolumeReplicaCondInQuorumReasonQuorumLost
		}
	} else {
		switch {
		case inQuorum && oldCond.Status != v1.ConditionTrue:
			// switch to true
			newCond.Status, newCond.Reason = v1.ConditionTrue, ReplicatedVolumeReplicaCondInQuorumReasonInQuorum
			newCond.Message = fmt.Sprintf("Quorum achieved after being lost for %v", time.Since(oldCond.LastTransitionTime.Time))

		case !inQuorum && oldCond.Status != v1.ConditionFalse:
			// switch to false
			newCond.Status, newCond.Reason = v1.ConditionFalse, ReplicatedVolumeReplicaCondInQuorumReasonQuorumLost
			newCond.Message = fmt.Sprintf("Quorum lost after being achieved for %v", time.Since(oldCond.LastTransitionTime.Time))
		default:
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
				Type:    ReplicatedVolumeReplicaCondInSyncType,
				Status:  v1.ConditionUnknown,
				Reason:  ReplicatedVolumeReplicaCondInSyncReasonUnknownDiskState,
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
				Type:    ReplicatedVolumeReplicaCondInSyncType,
				Status:  v1.ConditionUnknown,
				Reason:  ReplicatedVolumeReplicaCondInSyncReasonReplicaNotInitialized,
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

	newCond := v1.Condition{Type: ReplicatedVolumeReplicaCondInSyncType}
	newCond.ObservedGeneration = rvr.Generation

	oldCond := meta.FindStatusCondition(rvr.Status.Conditions, ReplicatedVolumeReplicaCondInSyncType)

	if oldCond == nil || oldCond.Status == v1.ConditionUnknown {
		// initial setup - simpler message
		if inSync {
			newCond.Status, newCond.Reason = v1.ConditionTrue, reasonForStatusTrue(diskful)
		} else {
			newCond.Status, newCond.Reason = v1.ConditionFalse, reasonForStatusFalseFromDiskState(device.DiskState)
		}
	} else {
		switch {
		case inSync && oldCond.Status != v1.ConditionTrue:
			// switch to true
			newCond.Status, newCond.Reason = v1.ConditionTrue, reasonForStatusTrue(diskful)
			newCond.Message = fmt.Sprintf(
				"Became synced after being not in sync with reason %s for %v",
				oldCond.Reason,
				time.Since(oldCond.LastTransitionTime.Time),
			)
		case !inSync && oldCond.Status != v1.ConditionFalse:
			// switch to false
			newCond.Status, newCond.Reason = v1.ConditionFalse, reasonForStatusFalseFromDiskState(device.DiskState)
			newCond.Message = fmt.Sprintf(
				"Became unsynced after being synced for %v",
				time.Since(oldCond.LastTransitionTime.Time),
			)
		default:
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
		Type:               ReplicatedVolumeReplicaCondConfiguredType,
		ObservedGeneration: rvr.Generation,
		Status:             v1.ConditionTrue,
		Reason:             ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
		Message:            "Configuration has been successfully applied",
	}

	if rvr.Status.DRBD.Errors != nil {
		switch {
		case rvr.Status.DRBD.Errors.FileSystemOperationError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReplicatedVolumeReplicaCondConfiguredReasonFileSystemOperationFailed
			cond.Message = rvr.Status.DRBD.Errors.FileSystemOperationError.Message
		case rvr.Status.DRBD.Errors.ConfigurationCommandError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReplicatedVolumeReplicaCondConfiguredReasonConfigurationCommandFailed
			cond.Message = fmt.Sprintf(
				"Command %s exited with code %d",
				rvr.Status.DRBD.Errors.ConfigurationCommandError.Command,
				rvr.Status.DRBD.Errors.ConfigurationCommandError.ExitCode,
			)
		case rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReplicatedVolumeReplicaCondConfiguredReasonSharedSecretAlgSelectionFailed
			cond.Message = fmt.Sprintf(
				"Algorithm %s is not supported by node kernel",
				rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError.UnsupportedAlg,
			)
		case rvr.Status.DRBD.Errors.LastPrimaryError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReplicatedVolumeReplicaCondConfiguredReasonPromoteFailed
			cond.Message = fmt.Sprintf(
				"Command %s exited with code %d",
				rvr.Status.DRBD.Errors.LastPrimaryError.Command,
				rvr.Status.DRBD.Errors.LastPrimaryError.ExitCode,
			)
		case rvr.Status.DRBD.Errors.LastSecondaryError != nil:
			cond.Status = v1.ConditionFalse
			cond.Reason = ReplicatedVolumeReplicaCondConfiguredReasonDemoteFailed
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

func (rvr *ReplicatedVolumeReplica) ComputeStatusConditionAttached(shouldBePrimary bool) (v1.Condition, error) {
	if rvr.Spec.Type != ReplicaTypeAccess && rvr.Spec.Type != ReplicaTypeDiskful {
		return v1.Condition{
			Type:   ReplicatedVolumeReplicaCondAttachedType,
			Status: v1.ConditionFalse,
			Reason: ReplicatedVolumeReplicaCondAttachedReasonAttachingNotApplicable,
		}, nil
	}

	if rvr.Spec.NodeName == "" || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
		return v1.Condition{
			Type:   ReplicatedVolumeReplicaCondAttachedType,
			Status: v1.ConditionUnknown,
			Reason: ReplicatedVolumeReplicaCondAttachedReasonAttachingNotInitialized,
		}, nil
	}

	isPrimary := rvr.Status.DRBD.Status.Role == "Primary"

	cond := v1.Condition{Type: ReplicatedVolumeReplicaCondAttachedType}

	if isPrimary {
		cond.Status = v1.ConditionTrue
		cond.Reason = ReplicatedVolumeReplicaCondAttachedReasonAttached
	} else {
		cond.Status = v1.ConditionFalse
		if shouldBePrimary {
			cond.Reason = ReplicatedVolumeReplicaCondAttachedReasonAttachPending
		} else {
			cond.Reason = ReplicatedVolumeReplicaCondAttachedReasonDetached
		}
	}

	return cond, nil
}

func (rvr *ReplicatedVolumeReplica) UpdateStatusConditionAttached(shouldBePrimary bool) error {
	cond, err := rvr.ComputeStatusConditionAttached(shouldBePrimary)
	if err != nil {
		return err
	}
	meta.SetStatusCondition(&rvr.Status.Conditions, cond)

	return nil
}

func (rvr *ReplicatedVolumeReplica) validateStatusDRBDNotNil() error {
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
		return ReplicatedVolumeReplicaCondInSyncReasonInSync
	}
	return ReplicatedVolumeReplicaCondInSyncReasonDiskless
}

func reasonForStatusFalseFromDiskState(diskState DiskState) string {
	switch diskState {
	case DiskStateDiskless:
		return ReplicatedVolumeReplicaCondInSyncReasonDiskLost
	case DiskStateAttaching:
		return ReplicatedVolumeReplicaCondInSyncReasonAttaching
	case DiskStateDetaching:
		return ReplicatedVolumeReplicaCondInSyncReasonDetaching
	case DiskStateFailed:
		return ReplicatedVolumeReplicaCondInSyncReasonFailed
	case DiskStateNegotiating:
		return ReplicatedVolumeReplicaCondInSyncReasonNegotiating
	case DiskStateInconsistent:
		return ReplicatedVolumeReplicaCondInSyncReasonInconsistent
	case DiskStateOutdated:
		return ReplicatedVolumeReplicaCondInSyncReasonOutdated
	default:
		return ReplicatedVolumeReplicaCondInSyncReasonUnknownDiskState
	}
}

func validateArgNotNil(arg any, argName string) error {
	if arg == nil {
		return fmt.Errorf("expected '%s' to be non-nil", argName)
	}
	// Check for typed nil pointers (e.g., (*SomeStruct)(nil) passed as any)
	v := reflect.ValueOf(arg)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return fmt.Errorf("expected '%s' to be non-nil", argName)
	}
	return nil
}
