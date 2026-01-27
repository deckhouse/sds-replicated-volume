package drbd

import "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"

func applyReportState(
	_ ActualState,
	_ *v1alpha1.DRBDResource,
	_ error,
	_ []Action,
) bool {
	// TODO Observed generation bumped here
	return false
}
