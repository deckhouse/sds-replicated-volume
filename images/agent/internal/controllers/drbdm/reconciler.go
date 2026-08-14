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
	"strings"
	"time"

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

// openerRetryInterval paces the wait for a dm device's openers to go away.
// Short, because the common case is udev probing a device that was just created.
const openerRetryInterval = 200 * time.Millisecond

// isDeviceBusyErr reports whether a dmsetup failure is the device still being
// held open. Every dmsetup failure exits 1, so EBUSY is only distinguishable
// from the message the ioctl produced.
func isDeviceBusyErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Device or resource busy")
}

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
// Waits, by requeueing, while a device is still held open.
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
		// Waiting for the openers to go, not a failure: consumers hold the device
		// until they are done with it, and udev briefly probes a freshly created
		// one. Requeue quietly rather than reporting an error the operator would
		// have to interpret.
		msg := fmt.Sprintf("upper device has %d opener(s), cannot remove", upperInfo.OpenCount)
		r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInUse, msg)
		if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
			return rf.Fail(patchErr)
		}
		rf.Log().Info("Waiting for upper device openers before removal",
			"openCount", upperInfo.OpenCount)
		return rf.DoneAndRequeueAfter(openerRetryInterval)
	}

	if upperInfo != nil {
		if err := dmsetup.Remove(rf.Ctx(), obj.Name); err != nil {
			// An opener can arrive between the count above and this ioctl, so
			// busy here means the same thing it does there: wait.
			if isDeviceBusyErr(err) {
				r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInUse, err.Error())
				if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
					return rf.Fail(patchErr)
				}
				rf.Log().Info("Upper device busy on removal, retrying", "device", obj.Name)
				return rf.DoneAndRequeueAfter(openerRetryInterval)
			}
			r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonRemoveFailed, err.Error())
			if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
				return rf.Fail(patchErr)
			}
			return rf.Fail(err)
		}
	}

	internalName := v1alpha1.FormatDRBDMapperInternalDeviceName(obj.Name)
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
			if isDeviceBusyErr(err) {
				r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonDeviceInUse, err.Error())
				if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
					return rf.Fail(patchErr)
				}
				rf.Log().Info("Internal device busy on removal, retrying", "device", internalName)
				return rf.DoneAndRequeueAfter(openerRetryInterval)
			}
			r.setConditionFalse(obj, v1alpha1.DRBDMapperCondConfiguredReasonRemoveFailed, err.Error())
			if patchErr := r.patchStatus(rf.Ctx(), obj); patchErr != nil {
				return rf.Fail(patchErr)
			}
			return rf.Fail(err)
		}
	}

	// The DRBDResource controller gates on this object, not on the dm layers, so
	// nothing may wake it until the finalizer below actually lets the object go.
	// The delete event does that, from this controller's own handler.
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

	internalName := v1alpha1.FormatDRBDMapperInternalDeviceName(obj.Name)

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
		if err := dmsetup.Create(rf.Ctx(), obj.Name, v1alpha1.FormatDRBDMapperInternalDevicePath(obj.Name)); err != nil {
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
		obj.Status.UpperDevicePath = v1alpha1.FormatDRBDMapperUpperDevicePath(obj.Name)
		obj.Status.OpenCount = int32(upperInfo.OpenCount)
		obju.SetStatusCondition(obj, metav1.Condition{
			Type:   v1alpha1.DRBDMapperCondConfiguredType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.DRBDMapperCondConfiguredReasonConfigured,
		})

		suspendedStatus, suspendedReason := metav1.ConditionFalse, v1alpha1.DRBDMapperCondIOSuspendedReasonActive
		if upperInfo.State == dmsetup.StateSuspended {
			suspendedStatus, suspendedReason = metav1.ConditionTrue, v1alpha1.DRBDMapperCondIOSuspendedReasonSuspended
		}
		obju.SetStatusCondition(obj, metav1.Condition{
			Type:   v1alpha1.DRBDMapperCondIOSuspendedType,
			Status: suspendedStatus,
			Reason: suspendedReason,
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
		obju.SetStatusCondition(obj, metav1.Condition{
			Type:    v1alpha1.DRBDMapperCondIOSuspendedType,
			Status:  metav1.ConditionUnknown,
			Reason:  v1alpha1.DRBDMapperCondIOSuspendedReasonDeviceAbsent,
			Message: "upper device does not exist",
		})
	}

	if !equality.Semantic.DeepEqual(obj.Status, base.Status) {
		if err := r.patchStatus(rf.Ctx(), obj); err != nil {
			return rf.Fail(err)
		}
		// The DRBDResource publishes this object's upper device to consumers. It
		// watches DRBDMapper, so the status write above is the notification.
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

// patchMain writes the main resource. As with patchStatus, an object that is
// already gone needs neither its finalizer added nor removed, so NotFound is
// success.
func (r *Reconciler) patchMain(
	ctx context.Context,
	obj, base *v1alpha1.DRBDMapper,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	return client.IgnoreNotFound(r.cl.Patch(ctx, obj, patch))
}

// patchStatus writes the status subresource. A object deleted underneath us has
// no status left to report, so NotFound is success: the next event settles it.
func (r *Reconciler) patchStatus(
	ctx context.Context,
	obj *v1alpha1.DRBDMapper,
) error {
	return client.IgnoreNotFound(r.cl.Status().Update(ctx, obj))
}
