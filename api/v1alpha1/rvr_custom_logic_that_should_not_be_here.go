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
	"slices"
	"strconv"
	"strings"
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
