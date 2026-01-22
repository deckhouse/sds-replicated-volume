/*
Copyright 2025 Flant JSC

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

package drbd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

type Reconciler struct {
	cl       client.Client
	log      *slog.Logger
	nodeName string
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log *slog.Logger, nodeName string) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		cl:       cl,
		log:      log.With("nodeName", nodeName),
		nodeName: nodeName,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	log := r.log.With("name", req.Name)
	log.Debug("Reconciling DRBDResource")

	dr, ok, err := r.getCurrentNodeDRBDResource(rf.Ctx(), log, req)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if !ok {
		return rf.Done().ToCtrl()
	}

	var (
		drbdResName       string = dr.DRBDResourceNameOnTheNode()
		iErr, aErr, aErr2 error
		drbdErr, patchErr error
		iState            IntendedState
		aState            ActualState
		tgtStateActions   TargetStateActions
	)

	iState, iErr = getIntendedState(dr)

	aState, aErr = getActualState(rf.Ctx(), drbdResName)

	// iState/aState will have `.IsZero() == true` in case of errors
	tgtStateActions = computeTargetStateActions(iState, aState)

	var patchNeeded, patchStatusNeeded, refreshActualNeeded bool
	original := client.MergeFrom(dr)

	for len(tgtStateActions) > 0 {
		switch ta := tgtStateActions[0].(type) {
		case PatchAction:
			patchNeeded = ta.ApplyPatch(dr) || patchNeeded
		case PatchStatusAction:
			patchStatusNeeded = ta.ApplyStatusPatch(dr) || patchStatusNeeded
		case ExecuteDRBDAction:
			// flush pending K8S patches before executing DRBD commands
			var resetOriginalNeeded bool
			if patchNeeded {
				if prepatchErr := r.cl.Patch(rf.Ctx(), dr, original); prepatchErr != nil {
					return rf.Failf(prepatchErr, "prepatching").ToCtrl()
				}
				patchNeeded = false
				resetOriginalNeeded = true
			}

			if patchStatusNeeded {
				if prepatchErr := r.cl.Status().Patch(rf.Ctx(), dr, original); prepatchErr != nil {
					return rf.Failf(prepatchErr, "prepatching status").ToCtrl()
				}
				patchStatusNeeded = false
				resetOriginalNeeded = true
			}

			if resetOriginalNeeded {
				original = client.MergeFrom(dr)
			}

			// execute
			drbdErr = ta.Execute(rf.Ctx())
			if drbdErr != nil {
				// leave failed along with non-executed actions in tgtState
				break
			}
			refreshActualNeeded = true
		}
		// pop successful action
		tgtStateActions = tgtStateActions[1:]
	}

	if refreshActualNeeded {
		aState, aErr2 = getActualState(rf.Ctx(), drbdResName)
	}

	if patchNeeded {
		patchErr = errors.Join(patchErr, r.cl.Patch(rf.Ctx(), dr, original))
	}

	err = errors.Join(iErr, aErr, aErr2, drbdErr, patchErr)

	patchStatusNeeded = applyReportState(
		aState,
		dr,
		err,
		tgtStateActions,
	) || patchStatusNeeded

	if patchStatusNeeded {
		patchErr = errors.Join(patchErr, r.cl.Status().Patch(rf.Ctx(), dr, original))
		err = errors.Join(err, patchErr)
	}

	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	return rf.Done().ToCtrl()
}

func (r *Reconciler) getCurrentNodeDRBDResource(
	ctx context.Context,
	log *slog.Logger,
	req reconcile.Request,
) (*v1alpha1.DRBDResource, bool, error) {
	dr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, dr); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, false, u.LogError(log, fmt.Errorf("getting DRBDResource: %w", err))
		}
		log.Debug("DRBDResource not found, skipping")
		return nil, false, nil
	}

	if dr.Spec.NodeName != r.nodeName {
		log.Debug(
			"DRBDResource belongs to different node, skipping",
			"nodeName", dr.Spec.NodeName,
		)
		return nil, false, nil
	}

	return dr, true, nil
}
