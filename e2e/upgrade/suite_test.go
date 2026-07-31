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

// Package upgrade holds the optional module-upgrade e2e suite. It installs the
// module at one image tag, fills a storage class with r3 volumes that are under
// continuous I/O, retags the module to a second image tag and finally migrates
// every volume from r3 to r2 — all against one running cluster, in this order,
// in a single Ordered container.
//
// The suite is OPTIONAL: without the two tags it is skipped as a whole, before
// Ginkgo is even started. See README.md for the environment contract and for the
// run line (it needs an explicit --timeout and --procs=1).
package upgrade

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
)

// Environment variables owned by this suite. The two upgrade variables the
// FRAMEWORK owns are named by the framework instead, so that each literal is
// spelled exactly once: fw.EnvUpgradeVolumes (parsed here, in the gate) and
// fw.EnvUpgradeImage (read by the writer pod helper). The opt-in class variables
// are fw.EnvAllowDisruptive / fw.EnvRunAll.
const (
	// envFromTag is the image tag the module is installed at before anything is
	// created: the OLD version of the upgrade under test.
	envFromTag = "E2E_UPGRADE_FROM_TAG"
	// envToTag is the image tag the module is retagged to mid-run: the NEW one.
	envToTag = "E2E_UPGRADE_TO_TAG"
	// envVolumeSize is the size of one volume, a Kubernetes quantity.
	envVolumeSize = "E2E_UPGRADE_VOLUME_SIZE"
	// envPoolType selects which of the two discovered pools the volumes live in.
	envPoolType = "E2E_UPGRADE_POOL_TYPE"
	// envMaxIOFreeze is the longest tolerated I/O freeze, a Go duration.
	envMaxIOFreeze = "E2E_UPGRADE_MAX_IO_FREEZE"
)

// The two spellings envPoolType accepts, mapped onto the pool types of the API.
const (
	poolTypeThin  = "thin"
	poolTypeThick = "thick"
)

const (
	// defaultMaxIOFreeze is the freeze tolerance when envMaxIOFreeze is unset. It
	// is the same 30s the framework's writers default to, so a freeze means the
	// same thing whichever writer reported it.
	defaultMaxIOFreeze = 30 * time.Second

	// moduleReadyBudget bounds each of the two module operations — the install of
	// the FROM version in the pre-discovery hook and the retag to the TO version
	// in phase B. It is passed to the helper explicitly rather than left to
	// default, because it is a budget a slow stand has to be able to raise, and a
	// number nobody can find is a number nobody raises. E2E_TIMEOUT_MULTIPLIER
	// does NOT scale it (it scales SpecTimeout only), so on a slow stand raise
	// this constant AND the suite's --timeout.
	moduleReadyBudget = fw.DefaultModuleReadyTimeout

	// diskfulReplicas is how many diskful replicas an r3 volume has, i.e. how many
	// usable diskful nodes the stand needs and how much pool space one volume
	// costs.
	diskfulReplicas = 3
)

// The environment of the run, validated ONCE by TestUpgrade before RunSpecs and
// read-only afterwards. They are package variables and not a struct because the
// three readers see them at three different moments: tree construction (budgetN),
// the pre-discovery hook (fromTag) and the specs.
//
// Nothing below re-reads the environment. In particular fw.EnvUpgradeVolumes is
// parsed exactly once, here, so the number that budgets the nodes and the number
// the volumes are created from cannot be read two different ways.
var (
	// fromTag and toTag are validated image tags, guaranteed to differ.
	fromTag string
	toTag   string
	// volumesOverride is fw.EnvUpgradeVolumes as ParseVolumesOverride read it: an
	// integer >= 1, or 0 for "not set, compute the count from the pool".
	volumesOverride int
	// volumeSize is a positive Kubernetes quantity.
	volumeSize = fw.DefaultVolumeSize
	// poolType is the pool the scenario runs in.
	poolType = v1alpha1.ReplicatedStoragePoolTypeLVMThin
	// maxIOFreeze is a positive duration.
	maxIOFreeze = defaultMaxIOFreeze
)

// f is the suite's framework instance, with the module bootstrap wired in front
// of discovery — see installFromVersion.
var f = fw.Setup(fw.WithPreDiscovery(installFromVersion))

// installFromVersion brings the module to fromTag and waits until it runs that
// build. It is the suite's pre-discovery hook, so it runs INSIDE
// SynchronizedBeforeSuite, before the framework detects the control plane and
// discovers the pools: both read objects that only exist once the module is
// installed, and a discovery that ran first would resolve the stand to the old
// control plane and cascade into skips.
//
// It always returns nil: EnsureModuleVersion reports a failure the framework way,
// by failing the running node, and a suite-level node is a perfectly good place
// for that (the hook's own error path exists for callers that have an error to
// return). Both outcomes stop the suite before a single spec runs.
func installFromVersion(ctx context.Context, f *fw.Framework) error {
	f.EnsureModuleVersion(ctx, fw.ModuleName, fromTag, moduleReadyBudget)
	return nil
}

// TestUpgrade is the suite's entry point and its gate.
//
// Everything the run needs from the environment is validated HERE, before
// RunSpecs — that is, before Ginkgo builds the spec tree and before
// SynchronizedBeforeSuite installs anything — and with the tools of `testing`,
// never with Ginkgo's: outside a running node Skip and Fail panic instead of
// skipping or failing.
//
// Two outcomes, deliberately different:
//
//   - Without the two tags the suite is SKIPPED. This is not a spec skip (the
//     suite's own specs never skip: a stand too small to run the scenario is a
//     failure, not a quieter run) — it is the refusal to run an OPTIONAL suite
//     that has nothing to compare, and the message says which variables to set.
//   - With a value that cannot be used the suite FAILS. A tag that is empty
//     after quoting, two identical tags (an "upgrade" that upgrades nothing), a
//     volume count that is not a number, the Disruptive class left off — each of
//     them would otherwise be discovered halfway through a run that already
//     retagged the module of a shared stand.
//
// The Disruptive check is the one that cannot be left to the framework. Every
// spec of this suite carries the class (through the container), and the class
// gate runs in JustBeforeEach — AFTER BeforeAll. Left to it, the suite would
// install the module, create up to twenty volumes with a pod each, and only then
// report every spec as skipped, with exit code 0.
func TestUpgrade(t *testing.T) {
	gate(t)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Module Upgrade E2E Suite")
}

// gate validates the environment and publishes it into the package variables.
// It is called by TestUpgrade only, and it never returns on a bad environment:
// t.Skipf and t.Fatalf both end the test where they stand.
//
// No t.Helper() on purpose: when this refuses a run, the line inside the gate is
// exactly what the reader needs to see.
func gate(t *testing.T) {
	fromTag = os.Getenv(envFromTag)
	toTag = os.Getenv(envToTag)

	var missing []string
	if fromTag == "" {
		missing = append(missing, envFromTag)
	}
	if toTag == "" {
		missing = append(missing, envToTag)
	}
	if len(missing) > 0 {
		t.Skipf("optional module-upgrade suite: %s not set."+
			" Export both %s (the image tag the module starts at) and %s (the tag it is"+
			" retagged to) to run it, for example %s=main %s=pr758.",
			strings.Join(missing, " and "), envFromTag, envToTag, envFromTag, envToTag)
	}

	if err := fw.ValidateModuleImageTag(fromTag); err != nil {
		t.Fatalf("%s %v", envFromTag, err)
	}
	if err := fw.ValidateModuleImageTag(toTag); err != nil {
		t.Fatalf("%s %v", envToTag, err)
	}
	if fromTag == toTag {
		t.Fatalf("%s and %s are both %q: the upgrade would degenerate into a no-op and the"+
			" suite would prove nothing. Name two different builds.",
			envFromTag, envToTag, fromTag)
	}

	if !fw.DisruptiveEnabled() {
		t.Fatalf("every spec of this suite carries the %s label (it retags the module of the"+
			" whole cluster), so the run needs %s=true (or %s=true). Without it the specs"+
			" would be skipped only AFTER the setup installed the module and created the"+
			" volumes, and the run would exit 0 having proven nothing.",
			fw.LabelDisruptive, fw.EnvAllowDisruptive, fw.EnvRunAll)
	}

	// The ONLY parse of fw.EnvUpgradeVolumes in the process. Everything
	// downstream — the node budgets computed on tree construction and the sizing
	// helper called in BeforeAll — takes the number, never the string.
	n, err := fw.ParseVolumesOverride(os.Getenv(fw.EnvUpgradeVolumes))
	if err != nil {
		t.Fatalf("%v", err)
	}
	volumesOverride = n

	if raw := os.Getenv(envVolumeSize); raw != "" {
		q, err := resource.ParseQuantity(raw)
		if err != nil {
			t.Fatalf("%s=%q is not a Kubernetes quantity (for example 1Gi): %v", envVolumeSize, raw, err)
		}
		if q.Sign() <= 0 {
			t.Fatalf("%s=%q must be positive", envVolumeSize, raw)
		}
		volumeSize = raw
	}

	switch raw := os.Getenv(envPoolType); raw {
	case "", poolTypeThin:
		poolType = v1alpha1.ReplicatedStoragePoolTypeLVMThin
	case poolTypeThick:
		poolType = v1alpha1.ReplicatedStoragePoolTypeLVM
	default:
		t.Fatalf("%s=%q must be %q or %q", envPoolType, raw, poolTypeThin, poolTypeThick)
	}

	if raw := os.Getenv(envMaxIOFreeze); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("%s=%q is not a Go duration (for example 45s): %v", envMaxIOFreeze, raw, err)
		}
		if d <= 0 {
			t.Fatalf("%s=%q must be positive", envMaxIOFreeze, raw)
		}
		maxIOFreeze = d
	}

	fmt.Fprintf(os.Stderr,
		"[upgrade] %s=%s -> %s=%s, pool=%s, volume size=%s, freeze tolerance=%s, volumes=%s\n",
		envFromTag, fromTag, envToTag, toTag, poolType, volumeSize, maxIOFreeze, volumesDescription())
}

// volumesDescription renders the volume count for the gate's log line.
func volumesDescription() string {
	if volumesOverride > 0 {
		return fmt.Sprintf("%d (%s)", volumesOverride, fw.EnvUpgradeVolumes)
	}
	return fmt.Sprintf("computed from the pool, at most %d", fw.VolumeCountCeiling)
}

// budgetN is the number of volumes the NODE BUDGETS are computed for. It is not
// the number of volumes that will exist: the real count is decided in BeforeAll,
// from the free space of the pool, and Ginkgo needs the budgets one phase
// earlier — SpecTimeout and NodeTimeout are decorator arguments, evaluated while
// the spec tree is built, which is before the suite has a cluster client at all.
//
// So the budgets are computed for the largest count the run can produce: the
// override when there is one, the ceiling of the clamp otherwise. The real count
// can then only be lower or equal, which is the invariant the budgets rely on.
//
// It reads no environment: the override was validated by the gate. And it MUST be
// called from a container BODY (tree construction), never from a package variable
// initializer — those run BEFORE TestUpgrade, where the override is still zero
// and unvalidated.
func budgetN() int {
	if volumesOverride > 0 {
		return volumesOverride
	}
	return fw.VolumeCountCeiling
}
