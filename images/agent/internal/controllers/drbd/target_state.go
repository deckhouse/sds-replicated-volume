package drbd

type TargetStateActions []Action

func computeTargetStateActions(iState IntendedState, aState ActualState) (res TargetStateActions) {
	// actions, which don't need neither [IntendedState], nor [ActualState]

	// (none)

	if iState.IsZero() {
		return
	}

	// actions, which don't need [ActualState]

	if iState.IsUpAndNotInCleanup() {
		res = append(res, AddAgentFinalizerAction{})
	} else {
		// should always go last
		defer func() {
			res = append(res, RemoveAgentFinalizerAction{})
		}()
	}

	res = append(res, ConfigureIPAddressAction{IPv4BySystemNetworkNames: iState.IPv4BySystemNetworkNames()})
	res = append(res, AllocatePortsAction{})

	if aState.IsZero() {
		return
	}

	// DRBD actions

	return nil
}
