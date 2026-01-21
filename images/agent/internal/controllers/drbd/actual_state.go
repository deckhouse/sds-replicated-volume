package drbd

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type ActualState interface {
	IsZero() bool

	ResourceName() string
}

// actualState represents the observed DRBD resource state.
type actualState struct {
	status *drbdsetup.Resource
	show   *drbdsetup.ShowResource
}

func (aState *actualState) IsZero() bool {
	return aState == nil
}

func (aState *actualState) ResourceName() string {
	return aState.status.Name
}

var _ ActualState = &actualState{}

func getActualState(ctx context.Context, drbdResName string) (*actualState, error) {
	statusResult, err := drbdsetup.ExecuteStatus(ctx, drbdResName)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup status: %w", err)
	}

	if len(statusResult) != 1 {
		// Resource not found in DRBD status - it might not be configured yet
		return nil, nil
	}

	// Get show output for configuration details
	showResult, err := drbdsetup.ExecuteShow(ctx, drbdResName)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup show: %w", err)
	}

	return &actualState{
		status: &statusResult[0],
		show:   showResult,
	}, nil
}
