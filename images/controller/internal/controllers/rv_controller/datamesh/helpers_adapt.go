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

package datamesh

// Thin wrappers around dmte.Adapt* that pin G=*globalContext, R=*ReplicaContext.
// Go cannot infer R from the input argument (R only appears in the return type),
// so these save call sites from spelling out explicit type parameters.

import (
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// asReplicaConfirm adapts a global-scoped confirm callback for use in ReplicaStep.
func asReplicaConfirm(fn dmte.GlobalConfirmFunc[*globalContext]) dmte.ReplicaConfirmFunc[*globalContext, *ReplicaContext] {
	return dmte.AdaptGlobalConfirm[*globalContext, *ReplicaContext](fn)
}

// asReplicaApply adapts a global-scoped apply callback for use in ReplicaStep.
func asReplicaApply(fn dmte.GlobalApplyFunc[*globalContext]) dmte.ReplicaApplyFunc[*globalContext, *ReplicaContext] {
	return dmte.AdaptGlobalApply[*globalContext, *ReplicaContext](fn)
}
