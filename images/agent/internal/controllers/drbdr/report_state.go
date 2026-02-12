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

package drbdr

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ensureReportState updates the DRBDResource status based on the actual DRBD state
// and any errors encountered during reconciliation.
// actualLLVName is the LLV name reverse-computed from the DRBD backing disk path.
func ensureReportState(
	ctx context.Context,
	aState ActualDRBDState,
	drbdr *v1alpha1.DRBDResource,
	actualLLVName string,
	err error,
	maintenanceMode bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "ensure-report-state")
	defer ef.OnEnd(&outcome)

	reportErr := aState.Report(drbdr)
	err = errors.Join(err, reportErr)
	applyConfiguredCondition(drbdr, err, maintenanceMode)

	// Set LVMLogicalVolumeName (reverse-computed from actual disk)
	if drbdr.Status.ActiveConfiguration == nil {
		drbdr.Status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{}
	}
	drbdr.Status.ActiveConfiguration.LVMLogicalVolumeName = actualLLVName

	return ef.Ok()
}

func applyConfiguredCondition(drbdr *v1alpha1.DRBDResource, err error, maintenanceMode bool) {
	var status metav1.ConditionStatus
	var reason, message string

	switch {
	case err != nil:
		status = metav1.ConditionFalse
		reason = getConfiguredReason(err)
		message = err.Error()
	case maintenanceMode:
		status = metav1.ConditionFalse
		reason = v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance
		message = "DRBD resource reconciliation is paused due to maintenance mode"
	default:
		status = metav1.ConditionTrue
		reason = v1alpha1.DRBDResourceCondConfiguredReasonConfigured
		message = ""
	}

	obju.SetStatusCondition(drbdr, metav1.Condition{
		Type:    v1alpha1.DRBDResourceCondConfiguredType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func getConfiguredReason(err error) string {
	var cfr ConfiguredReasonSource
	if errors.As(err, &cfr) {
		return cfr.ConfiguredReason()
	}
	return v1alpha1.DRBDResourceCondConfiguredReasonFailed
}
