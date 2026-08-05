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

package full

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
)

var f = fw.Setup()

// rolloutVolumes is how many volumes the configuration-rollout scenario migrates,
// and rolloutVolumesErr is what the framework said about a value it could not
// read. E2E_ROLLOUT_VOLUMES is parsed HERE and nowhere else, so the number that
// sizes the spec's budget and the number it creates volumes from cannot be read
// two different ways.
//
// A package variable and not a line in TestFull, because the spec tree is built
// while this package's variables are initialised — earlier than any test
// function runs — and the SpecTimeout sized from this count has to exist by
// then. The error waits for TestFull, which is a place that can stop the run.
var rolloutVolumes, rolloutVolumesErr = fw.ParseRolloutVolumes(os.Getenv(fw.EnvRolloutVolumes))

func TestFull(t *testing.T) {
	if rolloutVolumesErr != nil {
		t.Fatalf("%v", rolloutVolumesErr)
	}
	// A rollout scenario needs more volumes than the budget it is testing:
	// with no volume left over, none of them ever waits for a slot and the spec
	// would pass without observing the limit do anything.
	if rolloutVolumes <= rolloutMaxParallel {
		t.Fatalf("%s=%d must exceed the rollout budget of %d the scenario runs with,"+
			" otherwise no volume ever waits for a slot and the spec proves nothing;"+
			" leave it unset for the default of %d.",
			fw.EnvRolloutVolumes, rolloutVolumes, rolloutMaxParallel, fw.DefaultRolloutVolumes)
	}

	RegisterFailHandler(Fail)

	suiteConfig, reporterConfig := GinkgoConfiguration()
	if suiteConfig.LabelFilter == "" {
		suiteConfig.LabelFilter = "!/^Bug:/"
	}

	RunSpecs(t, "Full E2E Suite", suiteConfig, reporterConfig)
}
