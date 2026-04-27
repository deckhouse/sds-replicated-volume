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

package handlers

import (
	"context"
	"fmt"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// RVSValidate rejects ReplicatedVolumeSnapshot Create requests when the
// referenced ReplicatedVolume is currently in multi-primary state
// (≥2 attached diskful members in RV.Status.Datamesh.Members). The
// snapshot pipeline can only quiesce IO on a single primary at a time;
// a writer on a second primary would slip past SuspendIO/FlushBitmap
// and produce an inconsistent point-in-time image.
//
// Behavior:
//   - The check runs only on Create (Update/Delete pass through).
//   - If the parent RV does not exist yet, the request passes — the
//     rvs-controller will mark the RVS as Failed with
//     "ReplicatedVolume not found", and the webhook should not duplicate
//     that responsibility.
//   - The webhook intentionally does NOT defend against the race where
//     RV becomes multi-primary AFTER the RVS is admitted; that is the
//     rvs-controller's job (it triggers prepare-cleanup and lands the
//     RVS in Phase=Failed).
func RVSValidate(ctx context.Context, arReview *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	rvs, ok := obj.(*srv.ReplicatedVolumeSnapshot)
	if !ok {
		return &kwhvalidating.ValidatorResult{}, nil
	}
	if arReview.Operation != model.OperationCreate {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	cl, err := NewKubeClient("")
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	rv := &srv.ReplicatedVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: rvs.Spec.ReplicatedVolumeName}, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return &kwhvalidating.ValidatorResult{Valid: true}, nil
		}
		return nil, fmt.Errorf("failed to get ReplicatedVolume %q: %w", rvs.Spec.ReplicatedVolumeName, err)
	}

	if rv.IsMultiPrimary() {
		return &kwhvalidating.ValidatorResult{
			Valid: false,
			Message: fmt.Sprintf(
				"ReplicatedVolume %q is multi-primary (attached diskful members: %v); snapshots are not allowed",
				rv.Name, rv.AttachedDiskfulMembers()),
		}, nil
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}
