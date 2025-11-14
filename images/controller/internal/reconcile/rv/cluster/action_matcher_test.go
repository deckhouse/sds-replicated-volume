package cluster_test

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/go-cmp/cmp"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	cluster "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
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
// action matcher: [cluster.DeleteRVR]
//

type DeleteRVRMatcher struct {
	RVRName string
}

var _ ActionMatcher = DeleteRVRMatcher{}

func (m DeleteRVRMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.DeleteRVR](action)
	if err != nil {
		return err
	}

	if typedAction.RVR.Name() != m.RVRName {
		return newErrorf(
			"expected RVR to be deleted to have name '%s', got '%s'",
			m.RVRName, typedAction.RVR.Name(),
		)
	}
	return nil
}

//
// action matcher: [cluster.CreateRVR]
//

type CreateRVRMatcher struct {
	RVRSpec v1alpha2.ReplicatedVolumeReplicaSpec
}

var _ ActionMatcher = CreateRVRMatcher{}

func (m CreateRVRMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.CreateRVR](action)
	if err != nil {
		return err
	}

	// materialize object by applying initializer
	obj := &v1alpha2.ReplicatedVolumeReplica{}
	if typedAction.InitRVR == nil {
		return newErrorf("InitRVR is nil")
	}
	if err := typedAction.InitRVR(obj); err != nil {
		return err
	}

	if diff := cmp.Diff(m.RVRSpec, obj.Spec); diff != "" {
		return newErrorf("mismatch (-want +got):\n%s", diff)
	}

	return nil
}

//
// action matcher: [cluster.CreateLLV]
//

type CreateLLVMatcher struct {
	LLVSpec snc.LVMLogicalVolumeSpec
}

var _ ActionMatcher = CreateLLVMatcher{}

func (m CreateLLVMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.CreateLLV](action)
	if err != nil {
		return err
	}

	obj := &snc.LVMLogicalVolume{}
	if typedAction.InitLLV == nil {
		return newErrorf("InitLLV is nil")
	}
	if err := typedAction.InitLLV(obj); err != nil {
		return err
	}

	if diff := cmp.Diff(m.LLVSpec, obj.Spec); diff != "" {
		return newErrorf("mismatch (-want +got):\n%s", diff)
	}

	return nil
}

//
// action matcher: [cluster.DeleteLLV]
//

type DeleteLLVMatcher struct {
	LLVName string
}

var _ ActionMatcher = DeleteLLVMatcher{}

func (m DeleteLLVMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.DeleteLLV](action)
	if err != nil {
		return err
	}

	if typedAction.LLV.LLVName() != m.LLVName {
		return newErrorf(
			"expected LLV to be deleted to have name '%s', got '%s'",
			m.LLVName, typedAction.LLV.LLVName(),
		)
	}
	return nil
}

//
// action matcher: [cluster.PatchLLV]
//

type PatchLLVMatcher struct {
	LLVName string
	LLVSpec snc.LVMLogicalVolumeSpec
}

var _ ActionMatcher = PatchLLVMatcher{}

func (m PatchLLVMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.PatchLLV](action)
	if err != nil {
		return err
	}

	if typedAction.LLV.LLVName() != m.LLVName {
		return newErrorf(
			"expected LLV to be patched to have name '%s', got '%s'",
			m.LLVName, typedAction.LLV.LLVName(),
		)
	}

	// Simulate Apply and validate final state (spec)
	llvCopy := snc.LVMLogicalVolume{}
	llvCopy.Name = m.LLVName
	if typedAction.PatchLLV == nil {
		return newErrorf("PatchLLV is nil")
	}
	if err := typedAction.PatchLLV(&llvCopy); err != nil {
		return newErrorf("apply function returned error: %v", err)
	}

	if diff := cmp.Diff(m.LLVSpec, llvCopy.Spec); diff != "" {
		return newErrorf("mismatch (-want +got):\n%s", diff)
	}

	return nil
}

//
// action matcher: [cluster.PatchRVR]
//

type PatchRVRMatcher struct {
	RVRName string
}

var _ ActionMatcher = PatchRVRMatcher{}

func (m PatchRVRMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.PatchRVR](action)
	if err != nil {
		return err
	}

	if typedAction.RVR.Name() != m.RVRName {
		return newErrorf(
			"expected RVR to be patched to have name '%s', got '%s'",
			m.RVRName, typedAction.RVR.Name(),
		)
	}
	return nil
}
