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

package controllers

import (
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

// RegisterIndexes registers controller-runtime cache indexes used by controllers.
// It must be invoked before any controller starts listing with MatchingFields.
func RegisterIndexes(mgr manager.Manager) error {
	// ReplicatedVolume (RV)
	if err := indexes.RegisterRVByReplicatedStorageClassName(mgr); err != nil {
		return err
	}
	if err := indexes.RegisterRVByStoragePoolName(mgr); err != nil {
		return err
	}

	// ReplicatedVolumeAttachment (RVA)
	if err := indexes.RegisterRVAByReplicatedVolumeName(mgr); err != nil {
		return err
	}

	// ReplicatedVolumeReplica (RVR)
	if err := indexes.RegisterRVRByNodeName(mgr); err != nil {
		return err
	}
	if err := indexes.RegisterRVRByReplicatedVolumeName(mgr); err != nil {
		return err
	}
	if err := indexes.RegisterRVRByRVAndNode(mgr); err != nil {
		return err
	}

	// DRBDResource
	if err := indexes.RegisterDRBDResourceByNodeName(mgr); err != nil {
		return err
	}

	// ReplicatedStorageClass (RSC)
	if err := indexes.RegisterRSCByStoragePool(mgr); err != nil {
		return err
	}

	// ReplicatedStoragePool (RSP)
	if err := indexes.RegisterRSPByLVMVolumeGroupName(mgr); err != nil {
		return err
	}
	if err := indexes.RegisterRSPByEligibleNodeName(mgr); err != nil {
		return err
	}
	if err := indexes.RegisterRSPByUsedByRSCName(mgr); err != nil {
		return err
	}

	// LVMLogicalVolume (LLV)
	if err := indexes.RegisterLLVByRVROwner(mgr); err != nil {
		return err
	}

	// Pod
	if err := indexes.RegisterPodByNodeName(mgr); err != nil {
		return err
	}

	return nil
}
