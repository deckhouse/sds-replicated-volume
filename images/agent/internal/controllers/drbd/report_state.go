package drbd

import "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"

func applyReportState(
	aState ActualState,
	dr *v1alpha1.DRBDResource,
	existingErr error,
	incompleteActions []Action,
) bool {
	return false
}
