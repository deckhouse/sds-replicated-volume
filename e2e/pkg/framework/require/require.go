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

// Package require provides declarative test requirement decorators
// that return Ginkgo Labels with the Req: prefix. These labels are
// inherited from containers to children and enforced at runtime by
// the framework's JustBeforeEach hook.
package require

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Label prefixes and values used by requirement decorators.
const (
	LabelCPPrefix = "Req:ControlPlane:"
	LabelCPNew    = "Req:ControlPlane:New"
	LabelCPOld    = "Req:ControlPlane:Old"
	LabelCPAny    = "Req:ControlPlane:Any"
	LabelMinNodes = "Req:MinNodes:"
)

// MinNodes returns a Label requiring usable cluster nodes.
// Pool type defaults to LVMThin.
//
// The first argument (diskful) is the number of usable nodes with a ready
// LVG required. Optional arguments specify extra diskless nodes and pool type.
//
// Accepted call forms:
//
//	MinNodes(3)                     — 3 diskful nodes, 0 extra (LVMThin)
//	MinNodes(2, 1)                  — 2 diskful + 1 extra node (LVMThin)
//	MinNodes(3, v1alpha1.LVM)       — 3 diskful nodes (LVM thick)
//	MinNodes(2, 1, v1alpha1.LVM)    — 2 diskful + 1 extra node (LVM thick)
//
// Label format: Req:MinNodes:<diskful>:<extra>:<poolType>
func MinNodes(diskful int, args ...any) Labels {
	extra := 0
	poolType := v1alpha1.ReplicatedStoragePoolTypeLVMThin

	switch len(args) {
	case 0:
		// MinNodes(3) — diskful only, defaults
	case 1:
		// MinNodes(2, 1) or MinNodes(3, LVM)
		switch v := args[0].(type) {
		case int:
			extra = v
		case v1alpha1.ReplicatedStoragePoolType:
			poolType = v
		default:
			panic(fmt.Sprintf("MinNodes: unexpected argument type %T (expected int or ReplicatedStoragePoolType)", args[0]))
		}
	case 2:
		// MinNodes(2, 1, LVM)
		e, ok := args[0].(int)
		if !ok {
			panic(fmt.Sprintf("MinNodes: first optional argument must be int (extra count), got %T", args[0]))
		}
		extra = e
		pt, ok := args[1].(v1alpha1.ReplicatedStoragePoolType)
		if !ok {
			panic(fmt.Sprintf("MinNodes: second optional argument must be ReplicatedStoragePoolType, got %T", args[1]))
		}
		poolType = pt
	default:
		panic(fmt.Sprintf("MinNodes: too many arguments (expected 1-3, got %d)", 1+len(args)))
	}

	return Labels{fmt.Sprintf("%s%d:%d:%s", LabelMinNodes, diskful, extra, string(poolType))}
}

// OldControlPlane returns a Label requiring the old (pre-datamesh) control plane.
// The spec is skipped at runtime if the cluster runs the new control plane.
func OldControlPlane() Labels { return Labels{LabelCPOld} }

// AnyControlPlane returns a Label allowing any control plane version.
// No control plane check is performed at runtime.
func AnyControlPlane() Labels { return Labels{LabelCPAny} }
