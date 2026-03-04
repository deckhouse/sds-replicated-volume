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

package config

const (
	// ModuleNamespace is the Kubernetes namespace for the sds-replicated-volume module.
	ModuleNamespace = "d8-sds-replicated-volume"

	// MigrationConfigMapName is the name of the ConfigMap used to track migration state.
	MigrationConfigMapName = "control-plane-migration"

	// CSIDriverReplicated is the CSI driver name for replicated volumes.
	CSIDriverReplicated = "replicated.csi.storage.deckhouse.io"

	// LinstorCRDName is a CRD name used to detect whether LINSTOR is installed.
	LinstorCRDName = "nodes.internal.linstor.linbit.com"

	// NewControlPlaneCRDName is a CRD name used to detect whether the new control-plane is installed.
	NewControlPlaneCRDName = "replicatedvolumes.storage.deckhouse.io"

	// LinstorLVMSuffix is the suffix appended to PV names to form the LVM logical volume name in LINSTOR.
	LinstorLVMSuffix = "_00000"
)

// Migration state values written to ConfigMap.data.state.
const (
	StateNotStarted      = "not_started"
	StateStage1Started   = "stage1_started"
	StateStage1Completed = "stage1_completed"
	StateStage2Started   = "stage2_started"
	StateStage2Completed = "stage2_completed"
	StateStage3Started   = "stage3_started"
	StateAllCompleted    = "all_completed"
)
