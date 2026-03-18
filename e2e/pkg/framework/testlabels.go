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
const (
	LabelSmoke      = "Smoke"      // minimal set verifying core functionality
	LabelSlow       = "Slow"       // long-running; default SpecTimeout raised to 1min (vs 30s)
	LabelDisruptive = "Disruptive" // destructive actions (kill nodes, restart agents, reassign zones); auto-injects Serial + lowest SpecPriority, skipped unless E2E_ALLOW_DISRUPTIVE=true
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
