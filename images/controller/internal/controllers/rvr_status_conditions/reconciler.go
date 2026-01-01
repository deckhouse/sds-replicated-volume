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

package rvrstatusconditions

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Reconciler computes Online and IOReady conditions for ReplicatedVolumeReplica
type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.V(1).Info("Reconciling")

	// Get RVR
	// Note: continue even if DeletionTimestamp is set - finalizer controllers need fresh conditions
	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		// NotFound is expected, don't log as error
		if !errors.IsNotFound(err) {
			log.Error(err, "Getting ReplicatedVolumeReplica")
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Check agent availability and determine reason if not available
	agentReady, unavailabilityReason, shouldRetry := r.checkAgentAvailability(ctx, rvr.Spec.NodeName, log)

	// Calculate conditions
	onlineStatus, onlineReason, onlineMessage := r.calculateOnline(rvr, agentReady, unavailabilityReason)
	ioReadyStatus, ioReadyReason, ioReadyMessage := r.calculateIOReady(rvr, onlineStatus, agentReady, unavailabilityReason)

	// Update conditions if changed
	// setCondition modifies rvr in-memory and returns true if changed;
	// single Patch sends all changes together.
	// changed will be true even if only one of the conditions is changed.
	rvrCopy := rvr.DeepCopy()
	changed := false
	changed = r.setCondition(rvr, v1alpha1.ConditionTypeOnline, onlineStatus, onlineReason, onlineMessage) || changed
	changed = r.setCondition(rvr, v1alpha1.ConditionTypeIOReady, ioReadyStatus, ioReadyReason, ioReadyMessage) || changed

	if changed {
		log.V(1).Info("Updating conditions", "online", onlineStatus, "onlineReason", onlineReason, "ioReady", ioReadyStatus, "ioReadyReason", ioReadyReason)
		if err := r.cl.Status().Patch(ctx, rvr, client.MergeFrom(rvrCopy)); err != nil {
			if errors.IsNotFound(err) {
				log.V(1).Info("ReplicatedVolumeReplica was deleted during reconciliation, skipping patch")
				return reconcile.Result{}, nil
			}
			log.Error(err, "Patching RVR status")
			return reconcile.Result{}, err
		}
	}

	// If we couldn't determine agent status, trigger requeue
	if shouldRetry {
		return reconcile.Result{}, errors.NewServiceUnavailable("agent status unknown, retrying")
	}

	return reconcile.Result{}, nil
}

// checkAgentAvailability checks if the agent pod is available on the given node.
// Returns (agentReady, unavailabilityReason, shouldRetry).
// If shouldRetry is true, caller should return error to trigger requeue.
func (r *Reconciler) checkAgentAvailability(ctx context.Context, nodeName string, log logr.Logger) (bool, string, bool) {
	if nodeName == "" {
		return false, v1alpha1.ReasonUnscheduled, false
	}

	// AgentNamespace is taken from v1alpha1.ModuleNamespace
	// Agent pods run in the same namespace as controller
	agentNamespace := v1alpha1.ModuleNamespace

	// List agent pods on this node
	podList := &corev1.PodList{}
	if err := r.cl.List(ctx, podList,
		client.InNamespace(agentNamespace),
		client.MatchingLabels{AgentPodLabel: AgentPodValue},
	); err != nil {
		log.Error(err, "Listing agent pods, will retry")
		// Hybrid: set status to Unknown AND return error to requeue
		return false, v1alpha1.ReasonAgentStatusUnknown, true
	}

	// Find agent pod on this node (skip terminating pods)
	var agentPod *corev1.Pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		// Skip terminating pods (e.g., during rollout restart)
		if pod.DeletionTimestamp != nil {
			continue
		}
		agentPod = pod
		break
	}

	// No agent pod found on this node
	if agentPod == nil {
		// Check if it's a node issue or missing pod
		if r.isNodeNotReady(ctx, nodeName, log) {
			return false, v1alpha1.ReasonNodeNotReady, false
		}
		return false, v1alpha1.ReasonAgentPodMissing, false
	}

	// Check if agent pod is ready
	if agentPod.Status.Phase == corev1.PodRunning {
		for _, cond := range agentPod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, "", false
			}
		}
	}

	// Pod exists but not ready - check if node issue
	if r.isNodeNotReady(ctx, nodeName, log) {
		return false, v1alpha1.ReasonNodeNotReady, false
	}
	return false, v1alpha1.ReasonAgentNotReady, false
}

// isNodeNotReady checks if the node is not ready
func (r *Reconciler) isNodeNotReady(ctx context.Context, nodeName string, log logr.Logger) bool {
	node := &corev1.Node{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
		log.V(1).Info("Node not found, assuming NodeNotReady", "nodeName", nodeName)
		return true
	}

	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status != corev1.ConditionTrue
		}
	}
	return false
}

// calculateOnline computes the Online condition status, reason, and message.
// Online = Scheduled AND Initialized AND InQuorum
// Copies reason and message from source condition when False.
func (r *Reconciler) calculateOnline(rvr *v1alpha1.ReplicatedVolumeReplica, agentReady bool, unavailabilityReason string) (metav1.ConditionStatus, string, string) {
	// If agent/node is not available, return False with appropriate reason
	if !agentReady && unavailabilityReason != "" {
		return metav1.ConditionFalse, unavailabilityReason, ""
	}

	// Check Scheduled condition
	scheduledCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeScheduled)
	if scheduledCond == nil || scheduledCond.Status != metav1.ConditionTrue {
		reason, message := extractReasonAndMessage(scheduledCond, v1alpha1.ReasonUnscheduled, "Scheduled")
		return metav1.ConditionFalse, reason, message
	}

	// Check Initialized condition
	initializedCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeDataInitialized)
	if initializedCond == nil || initializedCond.Status != metav1.ConditionTrue {
		reason, message := extractReasonAndMessage(initializedCond, v1alpha1.ReasonUninitialized, "Initialized")
		return metav1.ConditionFalse, reason, message
	}

	// Check InQuorum condition
	inQuorumCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeInQuorum)
	if inQuorumCond == nil || inQuorumCond.Status != metav1.ConditionTrue {
		reason, message := extractReasonAndMessage(inQuorumCond, v1alpha1.ReasonQuorumLost, "InQuorum")
		return metav1.ConditionFalse, reason, message
	}

	return metav1.ConditionTrue, v1alpha1.ReasonOnline, ""
}

// calculateIOReady computes the IOReady condition status, reason, and message.
// IOReady = Online AND InSync
// Copies reason and message from source condition when False.
func (r *Reconciler) calculateIOReady(rvr *v1alpha1.ReplicatedVolumeReplica, onlineStatus metav1.ConditionStatus, agentReady bool, unavailabilityReason string) (metav1.ConditionStatus, string, string) {
	// If agent/node is not available, return False with appropriate reason
	if !agentReady && unavailabilityReason != "" {
		return metav1.ConditionFalse, unavailabilityReason, ""
	}

	// If not Online, IOReady is False with Offline reason
	if onlineStatus != metav1.ConditionTrue {
		return metav1.ConditionFalse, v1alpha1.ReasonOffline, ""
	}

	// Check InSync condition
	inSyncCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeInSync)
	if inSyncCond == nil || inSyncCond.Status != metav1.ConditionTrue {
		reason, message := extractReasonAndMessage(inSyncCond, v1alpha1.ReasonOutOfSync, "InSync")
		return metav1.ConditionFalse, reason, message
	}

	return metav1.ConditionTrue, v1alpha1.ReasonIOReady, ""
}

// setCondition sets a condition on the RVR and returns true if it was changed.
func (r *Reconciler) setCondition(rvr *v1alpha1.ReplicatedVolumeReplica, conditionType string, status metav1.ConditionStatus, reason, message string) bool {
	return meta.SetStatusCondition(&rvr.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rvr.Generation,
	})
}

// extractReasonAndMessage extracts reason and message from source condition.
// If source condition exists, copies its reason (or uses fallback) and adds prefixed message.
func extractReasonAndMessage(cond *metav1.Condition, fallbackReason, prefix string) (string, string) {
	if cond == nil {
		return fallbackReason, ""
	}

	reason := fallbackReason
	if cond.Reason != "" {
		reason = cond.Reason
	}

	message := ""
	if cond.Message != "" {
		message = prefix + ": " + cond.Message
	}

	return reason, message
}
