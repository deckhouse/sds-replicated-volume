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

// DRBDResourceState represents the desired state of a DRBD resource.
type DRBDResourceState string

const (
	// DRBDResourceStateUp indicates the resource should be up.
	DRBDResourceStateUp DRBDResourceState = "Up"
	// DRBDResourceStateDown indicates the resource should be down.
	DRBDResourceStateDown DRBDResourceState = "Down"
)

// DRBDRole represents the role of a DRBD resource.
type DRBDRole string

const (
	// DRBDRolePrimary indicates the resource is primary.
	DRBDRolePrimary DRBDRole = "Primary"
	// DRBDRoleSecondary indicates the resource is secondary.
	DRBDRoleSecondary DRBDRole = "Secondary"
)

// DRBDResourceType represents the type of a DRBD resource.
type DRBDResourceType string

const (
	// DRBDResourceTypeDiskful indicates a diskful resource that stores data.
	DRBDResourceTypeDiskful DRBDResourceType = "Diskful"
	// DRBDResourceTypeDiskless indicates a diskless resource.
	DRBDResourceTypeDiskless DRBDResourceType = "Diskless"
)

// DRBDProtocol represents the DRBD replication protocol.
type DRBDProtocol string

const (
	// DRBDProtocolA is asynchronous replication protocol.
	DRBDProtocolA DRBDProtocol = "A"
	// DRBDProtocolB is memory synchronous (semi-synchronous) replication protocol.
	DRBDProtocolB DRBDProtocol = "B"
	// DRBDProtocolC is synchronous replication protocol.
	DRBDProtocolC DRBDProtocol = "C"
)

// DRBDResourceOperationType represents the type of operation to perform on a DRBD resource.
type DRBDResourceOperationType string

const (
	// DRBDResourceOperationCreateNewUUID creates a new UUID for the resource.
	DRBDResourceOperationCreateNewUUID DRBDResourceOperationType = "CreateNewUUID"
	// DRBDResourceOperationForcePrimary forces the resource to become primary.
	DRBDResourceOperationForcePrimary DRBDResourceOperationType = "ForcePrimary"
	// DRBDResourceOperationInvalidate invalidates the resource data.
	DRBDResourceOperationInvalidate DRBDResourceOperationType = "Invalidate"
	// DRBDResourceOperationOutdate marks the resource as outdated.
	DRBDResourceOperationOutdate DRBDResourceOperationType = "Outdate"
	// DRBDResourceOperationVerify verifies data consistency with peers.
	DRBDResourceOperationVerify DRBDResourceOperationType = "Verify"
	// DRBDResourceOperationCreateSnapshot creates a snapshot of the resource.
	DRBDResourceOperationCreateSnapshot DRBDResourceOperationType = "CreateSnapshot"
)

// DRBDNodeOperationType represents the type of operation to perform on a DRBD node.
type DRBDNodeOperationType string

const (
	// DRBDNodeOperationUpdateDRBD updates DRBD on the node.
	DRBDNodeOperationUpdateDRBD DRBDNodeOperationType = "UpdateDRBD"
)
