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

package snapmesh

import (
	"iter"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

func prepareDispatcher() dmte.DispatchFunc[provider] {
	return func(cp provider) iter.Seq[dmte.DispatchDecision] {
		return func(yield func(dmte.DispatchDecision) bool) {
			gctx := cp.Global()
			if prepareCleanupNeeded(gctx) {
				log.FromContext(gctx.ctx).Info("prepare: dispatching cleanup plan", "plan", prepareCleanupPlanID)
				for i := range gctx.allReplicas {
					rctx := &gctx.allReplicas[i]
					if rctx.prepareTransition != nil && rctx.prepareTransition.PlanID == string(prepareCleanupPlanID) {
						continue
					}
					if !yield(dmte.DispatchReplica(rctx, prepareTransitionType, prepareCleanupPlanID)) {
						return
					}
				}
				return
			}
			for i := range gctx.allReplicas {
				rctx := &gctx.allReplicas[i]
				if rctx.prepareTransition != nil {
					continue
				}
				if !yield(dmte.DispatchReplica(rctx, prepareTransitionType, preparePlanID)) {
					return
				}
			}
		}
	}
}
