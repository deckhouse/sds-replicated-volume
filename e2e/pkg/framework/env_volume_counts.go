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
	"fmt"
	"strconv"
)

const (
	// EnvRolloutVolumes names the variable that sets how many volumes the
	// configuration-rollout scenario migrates in one storage class edit.
	//
	// The scenario is about a limit — how many volumes of a class may be
	// mid-rollout at a time — so the number of volumes it runs on is the scale of
	// what it proves: at the default it is ten waves of two, and a stand with less
	// room, or a person who wants one quick wave, needs to be able to say so
	// without editing the spec.
	//
	// The framework NEVER reads it. Like EnvUpgradeVolumes, it is parsed exactly
	// once per process — by a suite's entry point, through ParseRolloutVolumes —
	// and travels onwards as a number. The constant lives next to that parser so
	// the literal is spelled once and a message about the value can name the
	// variable it came from.
	EnvRolloutVolumes = "E2E_ROLLOUT_VOLUMES"

	// DefaultRolloutVolumes is how many volumes the configuration-rollout scenario
	// migrates when EnvRolloutVolumes is unset.
	//
	// Twenty against a budget of two is ten waves, which is the point: a single
	// wave shows that a limit exists, and only a queue that has to be served over
	// and over shows that it keeps holding — that a slot freed by a converged
	// volume goes to exactly one waiting volume, ten times in a row. It matches
	// VolumeCountCeiling, the largest count the sizing helper will plan for, so
	// the two scenarios put a comparable load on a stand.
	DefaultRolloutVolumes = 20
)

// ParseRolloutVolumes reads the value of E2E_ROLLOUT_VOLUMES: a decimal integer
// >= 1, or an empty string standing for "not set", which it reports as
// DefaultRolloutVolumes.
//
// This is the ONLY parser of that variable, and its only caller is meant to be a
// suite's entry point (func TestFull), before RunSpecs — hence an error rather
// than a Ginkgo failure, for the reason ParseVolumesOverride spells out.
//
// A value it cannot read comes back as the default TOGETHER with the error. The
// spec tree of a suite is built while the package's variables are initialised,
// which is earlier than any gate can run, and a spec sized from this count needs
// a usable number by then; the gate is what stops the run, so the number handed
// back on the error path is never used for anything but building a tree nobody
// executes.
func ParseRolloutVolumes(raw string) (int, error) {
	if raw == "" {
		return DefaultRolloutVolumes, nil
	}
	n, err := parseVolumeCountEnv(EnvRolloutVolumes, raw)
	if err != nil {
		return DefaultRolloutVolumes, err
	}
	return n, nil
}

// parseVolumeCountEnv reads a volume count out of one environment value: a
// decimal integer >= 1, named in every complaint by the variable it came from.
//
// Deliberately strict. Everything strconv.Atoi refuses — " 20" from a quoted
// shell variable, "1e2", "2.5", "twenty" — is refused here too rather than
// rounded or defaulted, because a count nobody meant is a scenario nobody meant:
// a suite that quietly ran on one volume, or on none, would report a green run
// for a scale it never reached.
func parseVolumeCountEnv(envName, raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q must be a decimal integer >= 1: %w", envName, raw, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s=%q must be a decimal integer >= 1", envName, raw)
	}
	return n, nil
}
