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

package v1alpha3

import (
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
