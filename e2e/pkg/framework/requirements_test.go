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

package framework

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
)

var _ = Describe("hasControlPlaneLabel", func() {
	It("returns true when Labels contain Req:ControlPlane:Old", func() {
		args := []any{Labels{require.LabelCPOld}}
		Expect(hasControlPlaneLabel(args)).To(BeTrue())
	})

	It("returns true when Labels contain Req:ControlPlane:Any", func() {
		args := []any{Labels{require.LabelCPAny}}
		Expect(hasControlPlaneLabel(args)).To(BeTrue())
	})

	It("returns true when Labels contain Req:ControlPlane:New", func() {
		args := []any{Labels{require.LabelCPNew}}
		Expect(hasControlPlaneLabel(args)).To(BeTrue())
	})

	It("returns false when no Labels present", func() {
		args := []any{"text", func() {}}
		Expect(hasControlPlaneLabel(args)).To(BeFalse())
	})

	It("returns false when Labels have no CP entry", func() {
		args := []any{Labels{"Smoke", "Req:MinNodes:3"}}
		Expect(hasControlPlaneLabel(args)).To(BeFalse())
	})

	It("finds CP label among multiple Labels args", func() {
		args := []any{Labels{"Smoke"}, Labels{require.LabelCPOld}}
		Expect(hasControlPlaneLabel(args)).To(BeTrue())
	})
})
