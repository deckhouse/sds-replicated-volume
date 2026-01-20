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

package chaos

// NodeInfo represents a node with its name and IP address
type NodeInfo struct {
	Name      string
	IPAddress string
}

// VMOperationType represents type of VM operation
type VMOperationType string

const (
	VMOperationRestart VMOperationType = "Restart"
	VMOperationStop    VMOperationType = "Stop"
	VMOperationStart   VMOperationType = "Start"
)

// ChaosType represents type of chaos scenario
type ChaosType string

const (
	ChaosTypeDRBDBlock        ChaosType = "drbd-block"
	ChaosTypeNetworkBlock     ChaosType = "network-block"
	ChaosTypeNetworkDegrade   ChaosType = "network-degrade"
	ChaosTypeVMReboot         ChaosType = "vm-reboot"
	ChaosTypeNetworkPartition ChaosType = "network-partition"
)

// Label keys for chaos resources
const (
	LabelChaosType      = "chaos.megatest/type"
	LabelChaosNodeA     = "chaos.megatest/node-a"
	LabelChaosNodeB     = "chaos.megatest/node-b"
	LabelChaosInterface = "chaos.megatest/interface"
	LabelChaosTargetIP  = "chaos.megatest/target-ip"
)
