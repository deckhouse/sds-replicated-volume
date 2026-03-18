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

package testkit

import (
	"fmt"

	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

type checkMode int

const (
	checkAlways checkMode = iota
	checkAfter
	checkWhile
)

// continuousCheck runs a matcher against every applicable snapshot.
type continuousCheck struct {
	mode         checkMode
	check        types.GomegaMatcher
	trigger      types.GomegaMatcher // checkAfter only
	condition    types.GomegaMatcher // checkWhile only
	active       bool
	registeredAt string
}

// evaluate runs the check against the given object. Returns an error
// on violation, nil otherwise. Called from injectEvent under lock.
func (c *continuousCheck) evaluate(obj client.Object) error {
	switch c.mode {
	case checkAlways:
		if !c.active {
			return nil
		}
		matched, detail := match.MatchObject(c.check, obj)
		if !matched {
			return fmt.Errorf("%s\n  Registered at: %s", detail, c.registeredAt)
		}

	case checkAfter:
		if !c.active {
			trigMatched, _ := match.MatchObject(c.trigger, obj)
			if trigMatched {
				c.active = true
			}
		}
		if c.active {
			matched, detail := match.MatchObject(c.check, obj)
			if !matched {
				return fmt.Errorf("%s\n  Registered at: %s", detail, c.registeredAt)
			}
		}

	case checkWhile:
		condMatched, _ := match.MatchObject(c.condition, obj)
		if !condMatched {
			return nil
		}
		matched, detail := match.MatchObject(c.check, obj)
		if !matched {
			return fmt.Errorf("%s\n  Registered at: %s", detail, c.registeredAt)
		}
	}

	return nil
}

// Always registers a check that runs on every snapshot from now.
// Use match.NewSwitch to wrap the matcher if you need to disable it later.
func (t *TrackedObject[T]) Always(m types.GomegaMatcher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checks = append(t.checks, &continuousCheck{
		mode:         checkAlways,
		check:        m,
		active:       true,
		registeredAt: callerLocation(2),
	})
}

// After registers a check that is dormant until trigger matches, then
// active forever.
// Use match.NewSwitch to wrap the matcher if you need to disable it later.
func (t *TrackedObject[T]) After(trigger, check types.GomegaMatcher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checks = append(t.checks, &continuousCheck{
		mode:         checkAfter,
		check:        check,
		trigger:      trigger,
		active:       false,
		registeredAt: callerLocation(2),
	})
}

// While registers a check that runs only on snapshots where condition
// is true.
// Use match.NewSwitch to wrap the matcher if you need to disable it later.
func (t *TrackedObject[T]) While(condition, check types.GomegaMatcher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checks = append(t.checks, &continuousCheck{
		mode:         checkWhile,
		check:        check,
		condition:    condition,
		active:       true,
		registeredAt: callerLocation(2),
	})
}
