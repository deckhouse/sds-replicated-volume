package cluster

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type Action interface {
	_action()
}

type Actions []Action

type ParallelActions []Action

func cleanAction(a Action) Action {
	switch t := a.(type) {
	case Actions:
		t = cleanActions(t)
		switch len(t) {
		case 0:
			return nil
		case 1:
			return t[0]
		default:
			return t
		}
	case ParallelActions:
		t = cleanActions(t)
		switch len(t) {
		case 0:
			return nil
		case 1:
			return t[0]
		default:
			return t
		}
	default:
		return a
	}
}

func cleanActions[T ~[]Action](actions T) (result T) {
	for _, a := range actions {
		a = cleanAction(a)
		if a == nil {
			continue
		}
		// ungroup items of same type
		if t, ok := a.(T); ok {
			result = append(result, t...)
		} else {
			result = append(result, a)
		}
	}
	return
}

type RVRWriter interface {
	WriteToRVR(rvr *v1alpha2.ReplicatedVolumeReplica) (ChangeSet, error)
}

type LLVWriter interface {
	WriteToLLV(llv *snc.LVMLogicalVolume) (ChangeSet, error)
}

type PatchRVR struct {
	RVR    RVRAdapter
	Writer RVRWriter
}

type PatchLLV struct {
	LLV    LLVAdapter
	Writer LLVWriter
}

// Creates RVR and waits for Ready=True status
// It should also initialize it, if needed
type CreateRVR struct {
	InitialSyncRequired bool
	Writer              RVRWriter
}

type DeleteRVR struct {
	RVR RVRAdapter
}

type CreateLLV struct {
	Writer LLVWriter
}

type DeleteLLV struct {
	LLV LLVAdapter
}

type ResizeRVR struct {
	RVR RVRAdapter
}

func (Actions) _action()         {}
func (ParallelActions) _action() {}
func (PatchRVR) _action()        {}
func (PatchLLV) _action()        {}
func (CreateRVR) _action()       {}
func (DeleteRVR) _action()       {}
func (CreateLLV) _action()       {}
func (DeleteLLV) _action()       {}
func (ResizeRVR) _action()       {}

var _ Action = Actions{}
var _ Action = ParallelActions{}

// ensure interface conformance
var _ Action = PatchRVR{}
var _ Action = PatchLLV{}
var _ Action = CreateRVR{}
var _ Action = DeleteRVR{}
var _ Action = CreateLLV{}
var _ Action = DeleteLLV{}
var _ Action = ResizeRVR{}
