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

import (
	"iter"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
)

// resizeDispatcher returns a dmte.DispatchFunc that triggers a ResizeVolume
// transition when the current datamesh size is smaller than the target size
// from rv.Spec.Size. Only grows — shrink is not supported.
func resizeDispatcher() dmte.DispatchFunc[provider] {
	return func(cp provider) iter.Seq[dmte.DispatchDecision] {
		return func(yield func(dmte.DispatchDecision) bool) {
			gctx := cp.Global()

			if gctx.datamesh.size.Cmp(drbd_size.AlignTo4Ki(gctx.size)) >= 0 {
				return
			}

			yield(dmte.DispatchGlobal(
				v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume,
				"resize/v1",
			))
		}
	}
}
