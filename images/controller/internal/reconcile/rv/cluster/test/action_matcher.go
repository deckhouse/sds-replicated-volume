package clustertest

import (
	"fmt"
	"reflect"

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
		return newErrorf("expected end of slice, got %d more actions", len(actions)-i)
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
}

var _ ActionMatcher = CreateReplicatedVolumeReplicaMatcher{}

func (m CreateReplicatedVolumeReplicaMatcher) Match(action cluster.Action) error {
	typedAction, err := matchType[cluster.CreateReplicatedVolumeReplica](action)
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(typedAction.ReplicatedVolumeReplica.Spec, m.RVRSpec) {
		return newErrorf(
			// TODO:
			"expected RVR to be created to be .., got ...",
		)
	}

	return nil
}
