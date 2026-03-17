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
)

// Label prefixes and values used by requirement decorators.
const (
	LabelCPPrefix = "Req:ControlPlane:"
	LabelCPNew    = "Req:ControlPlane:New"
	LabelCPOld    = "Req:ControlPlane:Old"
	LabelCPAny    = "Req:ControlPlane:Any"
	LabelMinNodes = "Req:MinNodes:"
)

// MinNodes returns a Label requiring at least n cluster nodes.
// The spec is skipped at runtime if the cluster has fewer nodes.
func MinNodes(n int) Labels { return Labels{fmt.Sprintf("%s%d", LabelMinNodes, n)} }

// OldControlPlane returns a Label requiring the old (pre-datamesh) control plane.
// The spec is skipped at runtime if the cluster runs the new control plane.
func OldControlPlane() Labels { return Labels{LabelCPOld} }

// AnyControlPlane returns a Label allowing any control plane version.
// No control plane check is performed at runtime.
func AnyControlPlane() Labels { return Labels{LabelCPAny} }
