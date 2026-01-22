package drbd

type TargetStateActions []Action

func computeTargetStateActions(iState IntendedState, aState ActualState) (res TargetStateActions) {
	// actions, which don't need neither [IntendedState], nor [ActualState]
	res = append(res, BumpObservedGenerationForConfiguredAction{})

	if iState.IsZero() {
		return
	}

	// TODO: adding finalizer BEFORE drbd actions

	// TODO: removing finalizer AFTER drbd actions

	// actions, which don't need [ActualState]
	if iState.IsUpAndNotInCleanup() {
		res = append(res, AddAgentFinalizerAction{})
	} else {
		// should always go last
		defer func() {
			res = append(res, RemoveAgentFinalizerAction{})
		}()
	}

	if aState.IsZero() {
		return
	}

	// DRBD actions

	return nil
}
