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

package drbdm

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/dmsetup"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// Reconciler reconciles DRBDMapper objects for the current node.
type Reconciler struct {
	cl       client.Client
	nodeName string
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, nodeName string) *Reconciler {
	return &Reconciler{
		cl:       cl,
		nodeName: nodeName,
	}
}

// Reconcile reconciles a DRBDMapper resource.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	rf.Log().V(1).Info("Reconciling DRBDMapper", "name", req.Name)

	obj, err := r.getDRBDMapper(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if obj == nil {
		return rf.Done().ToCtrl()
	}

	if obj.Spec.NodeName != r.nodeName {
		rf.Log().V(1).Info("DRBDMapper belongs to different node, skipping", "nodeName", obj.Spec.NodeName)
		return rf.Done().ToCtrl()
	}

	if obj.DeletionTimestamp != nil {
		return r.reconcileDelete(rf.Ctx(), obj).ToCtrl()
	}

	return r.reconcileNormal(rf.Ctx(), obj).ToCtrl()
}

// reconcileNormal handles the normal (non-deleting) reconciliation path.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileNormal(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "normal")
	defer rf.OnEnd(&outcome)

	outcome = r.reconcileEnsureFinalizer(rf.Ctx(), obj)
	if outcome.ShouldReturn() {
		return outcome
	}

	outcome = r.reconcileDevice(rf.Ctx(), obj)
	if outcome.ShouldReturn() {
		return outcome
	}

	outcome = r.reconcileStatus(rf.Ctx(), obj)
	return outcome
}

// reconcileDelete handles the deletion path: remove devices, remove finalizer.
// Refuses to remove devices when the upper device has openers.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileDelete(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "delete")
	defer rf.OnEnd(&outcome)

	if !obju.HasFinalizer(obj, v1alpha1.AgentFinalizer) {
		return rf.Done()
	}

	upperInfo, err := dmsetup.Info(rf.Ctx(), obj.Name)
	if err != nil {
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInfoFailed, err.Error())
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Fail(err)
	}

	if upperInfo != nil && upperInfo.OpenCount > 0 {
		msg := fmt.Sprintf("upper device has %d opener(s), cannot remove", upperInfo.OpenCount)
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInUse, msg)
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Fail(fmt.Errorf("%s", msg))
	}

	if upperInfo != nil {
		if err := dmsetup.Remove(rf.Ctx(), obj.Name); err != nil {
			r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonRemoveFailed, err.Error())
			if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
				return rf.Fail(patchErr)
			}
			return rf.Fail(err)
		}
	}

	internalName := InternalDeviceName(obj.Name)
	internalInfo, err := dmsetup.Info(rf.Ctx(), internalName)
	if err != nil {
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInfoFailed, err.Error())
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Fail(err)
	}

	if internalInfo != nil {
		if err := dmsetup.Remove(rf.Ctx(), internalName); err != nil {
			r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonRemoveFailed, err.Error())
			if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
				return rf.Fail(patchErr)
			}
			return rf.Fail(err)
		}
	}

	base := obj.DeepCopy()
	obju.RemoveFinalizer(obj, v1alpha1.AgentFinalizer)
	if err := r.patchMain(rf.Ctx(), obj, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Done()
}

// reconcileEnsureFinalizer adds the agent finalizer if not present.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileEnsureFinalizer(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "ensure-finalizer")
	defer rf.OnEnd(&outcome)

	base := obj.DeepCopy()
	changed := obju.AddFinalizer(obj, v1alpha1.AgentFinalizer)
	if changed {
		if err := r.patchMain(rf.Ctx(), obj, base); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// reconcileDevice creates the two-layer dm-linear devices if they don't exist.
// Layer 1 (internal): maps spec.lowerDevicePath
// Layer 2 (upper): maps the internal device, provides stable path to users
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDevice(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "device")
	defer rf.OnEnd(&outcome)

	internalName := InternalDeviceName(obj.Name)

	internalInfo, err := dmsetup.Info(rf.Ctx(), internalName)
	if err != nil {
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInfoFailed, err.Error())
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Fail(err)
	}

	if internalInfo == nil {
		if err := dmsetup.Create(rf.Ctx(), internalName, obj.Spec.LowerDevicePath); err != nil {
			r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonCreateFailed,
				fmt.Sprintf("creating internal device: %v", err))
			if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
				return rf.Fail(patchErr)
			}
			return rf.Fail(err)
		}
	}

	upperInfo, err := dmsetup.Info(rf.Ctx(), obj.Name)
	if err != nil {
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInfoFailed, err.Error())
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Fail(err)
	}

	if upperInfo == nil {
		if err := dmsetup.Create(rf.Ctx(), obj.Name, InternalDevicePath(obj.Name)); err != nil {
			r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonCreateFailed,
				fmt.Sprintf("creating upper device: %v", err))
			if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
				return rf.Fail(patchErr)
			}
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// reconcileStatus updates the status subresource with device info and conditions.
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	upperInfo, err := dmsetup.Info(rf.Ctx(), obj.Name)
	if err != nil {
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInfoFailed, err.Error())
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Fail(err)
	}

	base := obj.DeepCopy()

	if upperInfo != nil {
		obj.Status.UpperDevicePath = UpperDevicePath(obj.Name)
		obj.Status.OpenCount = int32(upperInfo.OpenCount)
		obju.SetStatusCondition(obj, metav1.Condition{
			Type:   v1alpha1.DRBDMapperCondConfiguredType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.DRBDMapperCondConfiguredReasonConfigured,
		})
	} else {
		obj.Status.UpperDevicePath = ""
		obj.Status.OpenCount = 0
		obju.SetStatusCondition(obj, metav1.Condition{
			Type:    v1alpha1.DRBDMapperCondConfiguredType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.DRBDMapperCondConfiguredReasonCreateFailed,
			Message: "upper device does not exist after creation attempt",
		})
	}

	if !equality.Semantic.DeepEqual(obj.Status, base.Status) {
		if err := r.patchStatus(rf.Ctx(), obj); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Done()
}

func (r *Reconciler) setConditionFalse(obj *v1alpha1.DRBDMapper, reason, message string) {
	obju.SetStatusCondition(obj, metav1.Condition{
		Type:    v1alpha1.DRBDMapperCondConfiguredType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func (r *Reconciler) getDRBDMapper(ctx context.Context, name string) (*v1alpha1.DRBDMapper, error) {
	obj := &v1alpha1.DRBDMapper{}
	err := r.cl.Get(ctx, client.ObjectKey{Name: name}, obj)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, flow.Wrapf(err, "getting DRBDMapper %q", name)
	}
	return obj, nil
}

func (r *Reconciler) patchMain(
	ctx context.Context,
	obj, base *v1alpha1.DRBDMapper,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) patchStatus(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) error {
	return r.cl.Status().Update(ctx, obj)
}
