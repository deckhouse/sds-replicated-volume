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

const (
	// DRBDResourceCondConfiguredType indicates whether the DRBD resource is configured.
	//
	// Status=True means the resource is fully configured and matches the intended state.
	// Status=False means configuration failed; see Reason for the failing step.
	DRBDResourceCondConfiguredType = "Configured"

	DRBDResourceCondConfiguredReasonAttachFailed          = "AttachFailed"
	DRBDResourceCondConfiguredReasonConfigured            = "Configured"
	DRBDResourceCondConfiguredReasonConnectFailed         = "ConnectFailed"
	DRBDResourceCondConfiguredReasonCreateMetadataFailed  = "CreateMetadataFailed"
	DRBDResourceCondConfiguredReasonDelPathFailed         = "DelPathFailed"
	DRBDResourceCondConfiguredReasonDelPeerFailed         = "DelPeerFailed"
	DRBDResourceCondConfiguredReasonDetachFailed          = "DetachFailed"
	DRBDResourceCondConfiguredReasonDisconnectFailed      = "DisconnectFailed"
	DRBDResourceCondConfiguredReasonDiskOptionsFailed     = "DiskOptionsFailed"
	DRBDResourceCondConfiguredReasonDownFailed            = "DownFailed"
	DRBDResourceCondConfiguredReasonFailed                = "Failed"
	DRBDResourceCondConfiguredReasonForgetPeerFailed      = "ForgetPeerFailed"
	DRBDResourceCondConfiguredReasonNetOptionsFailed      = "NetOptionsFailed"
	DRBDResourceCondConfiguredReasonNewMinorFailed        = "NewMinorFailed"
	DRBDResourceCondConfiguredReasonNewPathFailed         = "NewPathFailed"
	DRBDResourceCondConfiguredReasonNewPeerFailed         = "NewPeerFailed"
	DRBDResourceCondConfiguredReasonNewResourceFailed     = "NewResourceFailed"
	DRBDResourceCondConfiguredReasonPrimaryFailed         = "PrimaryFailed"
	DRBDResourceCondConfiguredReasonRenameFailed          = "RenameFailed"
	DRBDResourceCondConfiguredReasonResizeFailed          = "ResizeFailed"
	DRBDResourceCondConfiguredReasonResourceOptionsFailed = "ResourceOptionsFailed"
	DRBDResourceCondConfiguredReasonSecondaryFailed       = "SecondaryFailed"
	DRBDResourceCondConfiguredReasonStateQueryFailed      = "StateQueryFailed"
	DRBDResourceCondConfiguredReasonInMaintenance         = "InMaintenance"
)
