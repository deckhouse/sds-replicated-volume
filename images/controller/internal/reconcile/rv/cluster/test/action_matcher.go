package clustertest

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/go-cmp/cmp"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
)

type ActionMatcher interface {
	Match(action cluster.Action) error
}

//
// helpers: [errorf]
//

type errorf struct {
	format string
	args   []any
}

var _ error = errorf{}

func newErrorf(format string, a ...any) errorf {
	return errorf{format, a}
}

func (e errorf) Error() string {
	return fmt.Sprintf(e.format, e.args...)
}

//
// helpers: [matchType], [typeMismatchError]
//

func matchType[T any](val any) (T, error) {
	typedVal, ok := val.(T)
	if !ok {
		return typedVal, typeMismatchError[T]{val}
	}
	return typedVal, nil
}

type typeMismatchError[T any] struct {
	got any
}

var _ error = typeMismatchError[any]{}

func (e typeMismatchError[T]) Error() string {
	return fmt.Sprintf("expected action of type '%s', got '%T'", reflect.TypeFor[T]().Name(), e.got)
}

//
// action matcher: [cluster.Actions]
//

type ActionsMatcher []ActionMatcher

var _ ActionMatcher = ActionsMatcher{}

func (m ActionsMatcher) Match(action cluster.Action) error {
	actions, err := matchType[cluster.Actions](action)
	if err != nil {
		return err
	}

	var i int
	for ; i < len(m); i++ {
		if len(actions) == i {
			return newErrorf("expected action element to be matched by '%T', got end of slice", m[i])
		}
		if err := m[i].Match(actions[i]); err != nil {
			return err
		}
	}
	if i != len(actions) {
		extra := make([]string, 0, len(actions)-i)
		for _, a := range actions[i:] {
			extra = append(extra, fmt.Sprintf("%T", a))
		}
		return newErrorf("expected end of slice, got %d more actions: [%s]", len(actions)-i, strings.Join(extra, ", "))
	}

	return nil
}

//
// action matcher: [cluster.ParallelActions]
//

type ParallelActionsMatcher []ActionMatcher

var _ ActionMatcher = ParallelActionsMatcher{}

func (m ParallelActionsMatcher) Match(action cluster.Action) error {
	actions, err := matchType[cluster.ParallelActions](action)
	if err != nil {
		return err
	}

	// order is irrelevant

	if len(m) != len(actions) {
		return newErrorf("expected %d parallel actions, got %d", len(m), len(actions))
	}

	matchedActions := make(map[int]struct{}, len(actions))
	for mIdx, mItem := range m {
		var matched bool
		for aIdx, aItem := range actions {
			if _, ok := matchedActions[aIdx]; ok {
				continue
			}
			err := mItem.Match(aItem)
			if err == nil {
				matched = true
				matchedActions[aIdx] = struct{}{}
				break
			}
		}

		if !matched {
			return newErrorf("parallel action matcher %T (index %d) didn't match any action", mItem, mIdx)
		}
	}

	return nil
}

//
// action matcher: [cluster.DeleteReplicatedVolumeReplica]
//

type DeleteReplicatedVolumeReplicaMatcher struct {
	RVRName string
}

var _ ActionMatcher = DeleteReplicatedVolumeReplicaMatcher{}

func (m DeleteReplicatedVolumeReplicaMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.DeleteReplicatedVolumeReplica](action)
	if err != nil {
		return err
	}

	if typedAction.ReplicatedVolumeReplica.Name != m.RVRName {
		return newErrorf(
			"expected RVR to be deleted to have name '%s', got '%s'",
			m.RVRName, typedAction.ReplicatedVolumeReplica.Name,
		)
	}
	return nil
}

//
// action matcher: [cluster.CreateReplicatedVolumeReplica]
//

type CreateReplicatedVolumeReplicaMatcher struct {
	RVRSpec v1alpha2.ReplicatedVolumeReplicaSpec
	OnMatch func(action cluster.CreateReplicatedVolumeReplica)
}

var _ ActionMatcher = CreateReplicatedVolumeReplicaMatcher{}

func (m CreateReplicatedVolumeReplicaMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.CreateReplicatedVolumeReplica](action)
	if err != nil {
		return err
	}

	if diff := cmp.Diff(m.RVRSpec, typedAction.ReplicatedVolumeReplica.Spec); diff != "" {
		return newErrorf("mismatch (-want +got):\n%s", diff)
	}

	m.OnMatch(typedAction)

	return nil
}

//
// action matcher: [cluster.WaitReplicatedVolumeReplica]
//

type WaitReplicatedVolumeReplicaMatcher struct {
	RVRName string
}

var _ ActionMatcher = WaitReplicatedVolumeReplicaMatcher{}

func (m WaitReplicatedVolumeReplicaMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.WaitReplicatedVolumeReplica](action)
	if err != nil {
		return err
	}

	if typedAction.ReplicatedVolumeReplica.Name != m.RVRName {
		return newErrorf(
			"expected RVR to be waited to have name '%s', got '%s'",
			m.RVRName, typedAction.ReplicatedVolumeReplica.Name,
		)
	}
	return nil
}

//
// action matcher: [cluster.CreateLVMLogicalVolume]
//

type CreateLVMLogicalVolumeMatcher struct {
	LLVSpec snc.LVMLogicalVolumeSpec
	OnMatch func(action cluster.CreateLVMLogicalVolume)
}

var _ ActionMatcher = CreateLVMLogicalVolumeMatcher{}

func (m CreateLVMLogicalVolumeMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.CreateLVMLogicalVolume](action)
	if err != nil {
		return err
	}

	if diff := cmp.Diff(m.LLVSpec, typedAction.LVMLogicalVolume.Spec); diff != "" {
		return newErrorf("mismatch (-want +got):\n%s", diff)
	}

	m.OnMatch(typedAction)

	return nil
}

//
// action matcher: [cluster.WaitLVMLogicalVolume]
//

type WaitLVMLogicalVolumeMatcher struct {
	LLVName string
}

var _ ActionMatcher = WaitLVMLogicalVolumeMatcher{}

func (m WaitLVMLogicalVolumeMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.WaitLVMLogicalVolume](action)
	if err != nil {
		return err
	}

	if typedAction.LVMLogicalVolume.Name != m.LLVName {
		return newErrorf(
			"expected RVR to be waited to have name '%s', got '%s'",
			m.LLVName, typedAction.LVMLogicalVolume.Name,
		)
	}
	return nil
}

//
// action matcher: [cluster.LLVPatch]
//

type LLVPatchMatcher struct {
	LLVName  string
	Validate func(before, after *snc.LVMLogicalVolume) error
}

var _ ActionMatcher = LLVPatchMatcher{}

func (m LLVPatchMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.LLVPatch](action)
	if err != nil {
		return err
	}

	if typedAction.LVMLogicalVolume.Name != m.LLVName {
		return newErrorf(
			"expected LLV to be patched to have name '%s', got '%s'",
			m.LLVName, typedAction.LVMLogicalVolume.Name,
		)
	}

	// Simulate Apply to verify intended mutations
	before := *typedAction.LVMLogicalVolume
	llvCopy := *typedAction.LVMLogicalVolume
	if err := typedAction.Apply(&llvCopy); err != nil {
		return newErrorf("apply function returned error: %v", err)
	}

	if m.Validate != nil {
		if err := m.Validate(&before, &llvCopy); err != nil {
			return err
		}
	}

	return nil
}

//
// action matcher: [cluster.RVRPatch]
//

type RVRPatchMatcher struct {
	RVRName string
}

var _ ActionMatcher = RVRPatchMatcher{}

func (m RVRPatchMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.RVRPatch](action)
	if err != nil {
		return err
	}

	if typedAction.ReplicatedVolumeReplica.Name != m.RVRName {
		return newErrorf(
			"expected RVR to be patched to have name '%s', got '%s'",
			m.RVRName, typedAction.ReplicatedVolumeReplica.Name,
		)
	}
	return nil
}

//
// action matcher: [cluster.WaitAndTriggerInitialSync]
//

type WaitAndTriggerInitialSyncMatcher struct {
	RVRNames []string
}

var _ ActionMatcher = WaitAndTriggerInitialSyncMatcher{}

func (m WaitAndTriggerInitialSyncMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.WaitAndTriggerInitialSync](action)
	if err != nil {
		return err
	}

	if len(m.RVRNames) == 0 {
		return nil
	}

	expected := make(map[string]int, len(m.RVRNames))
	for _, name := range m.RVRNames {
		expected[name]++
	}

	for _, rvr := range typedAction.ReplicatedVolumeReplicas {
		if expected[rvr.Name] == 0 {
			return newErrorf("unexpected RVR in initial sync: '%s'", rvr.Name)
		}
		expected[rvr.Name]--
		if expected[rvr.Name] == 0 {
			delete(expected, rvr.Name)
		}
	}

	if len(expected) != 0 {
		return newErrorf("expected initial sync for RVRs: %v, got different set", m.RVRNames)
	}

	return nil
}
