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

// Speed / safety labels for Ginkgo --label-filter.
//
// Disruptive and LongHaul are the two opt-in classes: their specs are skipped
// unless the run enables them. Both gates read the same formula — the class
// variable OR the umbrella E2E_RUN_ALL, each parsed as a boolean
// (strconv.ParseBool), where false or an unrecognized value keeps the class
// skipped. Neither variable is set by any script or workflow in this
// repository; the person starting the run exports them.
const (
	LabelSmoke      = "Smoke"      // minimal set verifying core functionality
	LabelSlow       = "Slow"       // long-running; default SpecTimeout raised to 1min (vs 30s)
	LabelDisruptive = "Disruptive" // destructive actions (kill nodes, restart agents, reassign zones, remove a finalizer by hand, write to a raw device); auto-injects Serial + lowest SpecPriority, skipped unless E2E_ALLOW_DISRUPTIVE=true or E2E_RUN_ALL=true
	LabelLongHaul   = "LongHaul"   // very long opt-in specs (tens of minutes of waiting, e.g. an alert with for: 15m); default SpecTimeout raised to 30min, auto-injects the HIGHEST SpecPriority and NOT Serial, skipped unless E2E_ALLOW_LONG_HAUL=true or E2E_RUN_ALL=true (a focused run bypasses this gate)
	LabelUpgrade    = "Upgrade"    // tests migration from old (linstor based) to new control plane
)

// Feature labels for Ginkgo --label-filter.
const (
	LabelFeatureMembership = "Feature:Membership"
	LabelFeatureResize     = "Feature:Resize"
	LabelFeatureAttachment = "Feature:Attachment"
	LabelFeatureNetwork    = "Feature:Network"
	LabelFeatureRecovery   = "Feature:Recovery"
	LabelFeatureTopology   = "Feature:Topology"
	LabelFeatureQuorum     = "Feature:Quorum"
	LabelFeatureStatus     = "Feature:Status"
)
