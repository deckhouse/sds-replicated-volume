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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

type concurrencyTracker struct{}

func newTracker() *concurrencyTracker { return &concurrencyTracker{} }

func (*concurrencyTracker) Add(_ *dmte.Transition)    {}
func (*concurrencyTracker) Remove(_ *dmte.Transition)  {}
func (*concurrencyTracker) CanAdmit(_ *dmte.Transition) (bool, string, any) {
	return true, "", nil
}
