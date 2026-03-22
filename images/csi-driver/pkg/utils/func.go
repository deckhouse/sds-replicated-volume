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

package utils //nolint:revive // legacy package name

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
)

const (
	KubernetesAPIRequestLimit       = 3
	KubernetesAPIRequestTimeout     = 1
	SDSReplicatedVolumeCSIFinalizer = "sds-replicated-volume.deckhouse.io/csi-controller"
)

// CreateReplicatedVolume creates a ReplicatedVolume resource
func CreateReplicatedVolume(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name, pvcName, pvcNamespace string,
	rvSpec srv.ReplicatedVolumeSpec,
) (*srv.ReplicatedVolume, error) {
	rv := &srv.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			OwnerReferences: []metav1.OwnerReference{},
			Finalizers:      []string{SDSReplicatedVolumeCSIFinalizer},
			Annotations: map[string]string{
				srv.SchedulingReservationIDAnnotationKey:      pvcNamespace + "/" + pvcName,
				srv.ReplicatedVolumePVCNamespaceAnnotationKey: pvcNamespace,
			},
		},
		Spec: rvSpec,
	}

	log.Trace(fmt.Sprintf("[CreateReplicatedVolume][traceID:%s][volumeID:%s] ReplicatedVolume: %+v", traceID, name, rv))

	err := kc.Create(ctx, rv)
	return rv, err
}

// GetReplicatedVolume gets a ReplicatedVolume resource
func GetReplicatedVolume(ctx context.Context, kc client.Client, name string) (*srv.ReplicatedVolume, error) {
	rv := &srv.ReplicatedVolume{}
	err := kc.Get(ctx, client.ObjectKey{Name: name}, rv)
	return rv, err
}

func hasFormationTransition(transitions []srv.ReplicatedVolumeDatameshTransition) bool {
	for i := range transitions {
		if transitions[i].Type == srv.ReplicatedVolumeDatameshTransitionTypeFormation {
			return true
		}
	}
	return false
}

// WaitForReplicatedVolumeReady waits for ReplicatedVolume to become ready
func WaitForReplicatedVolumeReady(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
) (int, error) {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Waiting for ReplicatedVolume to become ready", traceID, name))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] context done. Failed to wait for ReplicatedVolume", traceID, name))
			return attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, name)
		if err != nil {
			return attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Attempt: %d, ReplicatedVolume: %+v", traceID, name, attemptCounter, rv))
		}

		if rv.DeletionTimestamp != nil {
			return attemptCounter, fmt.Errorf("failed to create ReplicatedVolume %s, reason: ReplicatedVolume is being deleted", name)
		}

		if rv.Status.DatameshRevision > 0 && !hasFormationTransition(rv.Status.DatameshTransitions) {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] ReplicatedVolume is ready (datameshRevision=%d, no Formation transition)", traceID, name, rv.Status.DatameshRevision))
			return attemptCounter, nil
		}
		log.Trace(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Attempt %d, ReplicatedVolume not ready yet (datameshRevision=%d, hasFormation=%v). Waiting...", traceID, name, attemptCounter, rv.Status.DatameshRevision, hasFormationTransition(rv.Status.DatameshTransitions)))
	}
}

// DeleteReplicatedVolume deletes a ReplicatedVolume resource
func DeleteReplicatedVolume(ctx context.Context, kc client.Client, log *logger.Logger, traceID, name string) error {
	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] Trying to find ReplicatedVolume", traceID, name))
	rv, err := GetReplicatedVolume(ctx, kc, name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Info(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] ReplicatedVolume not found, already deleted", traceID, name))
			return nil
		}
		return fmt.Errorf("get ReplicatedVolume %s: %w", name, err)
	}

	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] ReplicatedVolume found: %+v", traceID, name, rv))
	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] Removing finalizer %s if exists", traceID, name, SDSReplicatedVolumeCSIFinalizer))

	removed, err := removervdeletepropagationIfExist(ctx, kc, log, rv, SDSReplicatedVolumeCSIFinalizer)
	if err != nil {
		return fmt.Errorf("remove finalizers from ReplicatedVolume %s: %w", name, err)
	}
	if removed {
		log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] finalizer %s removed from ReplicatedVolume %s", traceID, name, SDSReplicatedVolumeCSIFinalizer, name))
	} else {
		log.Warning(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] finalizer %s not found in ReplicatedVolume %s", traceID, name, SDSReplicatedVolumeCSIFinalizer, name))
	}

	log.Trace(fmt.Sprintf("[DeleteReplicatedVolume][traceID:%s][volumeID:%s] Trying to delete ReplicatedVolume", traceID, name))
	err = kc.Delete(ctx, rv)
	return err
}

// WaitForReplicatedVolumeDeleted waits for ReplicatedVolume to be fully deleted (NotFound).
func WaitForReplicatedVolumeDeleted(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
) (int, error) {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForReplicatedVolumeDeleted][traceID:%s][volumeID:%s] Waiting for ReplicatedVolume to be deleted", traceID, name))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForReplicatedVolumeDeleted][traceID:%s][volumeID:%s] context done", traceID, name))
			return attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		_, err := GetReplicatedVolume(ctx, kc, name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("[WaitForReplicatedVolumeDeleted][traceID:%s][volumeID:%s] ReplicatedVolume is fully deleted", traceID, name))
				return attemptCounter, nil
			}
			return attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeDeleted][traceID:%s][volumeID:%s] Attempt: %d, still waiting...", traceID, name, attemptCounter))
		}
	}
}

func removervdeletepropagationIfExist(ctx context.Context, kc client.Client, log *logger.Logger, rv *srv.ReplicatedVolume, finalizer string) (bool, error) {
	for attempt := 0; attempt < KubernetesAPIRequestLimit; attempt++ {
		removed := false
		for i, val := range rv.Finalizers {
			if val == finalizer {
				rv.Finalizers = slices.Delete(rv.Finalizers, i, i+1)
				removed = true
				break
			}
		}

		if !removed {
			return false, nil
		}

		log.Trace(fmt.Sprintf("[removervdeletepropagationIfExist] removing finalizer %s from ReplicatedVolume %s", finalizer, rv.Name))
		err := kc.Update(ctx, rv)
		if err == nil {
			return true, nil
		}

		if !kerrors.IsConflict(err) {
			return false, fmt.Errorf("[removervdeletepropagationIfExist] error updating ReplicatedVolume %s: %w", rv.Name, err)
		}

		if attempt < KubernetesAPIRequestLimit-1 {
			log.Trace(fmt.Sprintf("[removervdeletepropagationIfExist] conflict while updating ReplicatedVolume %s, retrying...", rv.Name))
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
				time.Sleep(KubernetesAPIRequestTimeout * time.Second)
				freshRV, getErr := GetReplicatedVolume(ctx, kc, rv.Name)
				if getErr != nil {
					return false, fmt.Errorf("[removervdeletepropagationIfExist] error getting ReplicatedVolume %s after update conflict: %w", rv.Name, getErr)
				}
				*rv = *freshRV
			}
		}
	}

	return false, fmt.Errorf("after %d attempts of removing finalizer %s from ReplicatedVolume %s, last error: %w", KubernetesAPIRequestLimit, finalizer, rv.Name, nil)
}

// GetReplicatedVolumeReplicaForNode gets ReplicatedVolumeReplica for a specific node
func GetReplicatedVolumeReplicaForNode(ctx context.Context, kc client.Client, volumeName, nodeName string) (*srv.ReplicatedVolumeReplica, error) {
	rvrList := &srv.ReplicatedVolumeReplicaList{}
	err := kc.List(
		ctx,
		rvrList,
		client.MatchingLabels{
			srv.ReplicatedVolumeLabelKey: volumeName,
		},
	)
	if err != nil {
		return nil, err
	}

	for i := range rvrList.Items {
		if rvrList.Items[i].Spec.NodeName == nodeName {
			return &rvrList.Items[i], nil
		}
	}

	return nil, fmt.Errorf("ReplicatedVolumeReplica not found for volume %s on node %s", volumeName, nodeName)
}

// GetDRBDDevicePath gets DRBD device path from ReplicatedVolumeReplica status
func GetDRBDDevicePath(rvr *srv.ReplicatedVolumeReplica) (string, error) {
	if rvr.Status.Attachment == nil {
		return "", fmt.Errorf("replica is not attached")
	}
	if rvr.Status.Attachment.DevicePath == "" {
		return "", fmt.Errorf("device path not available")
	}
	return rvr.Status.Attachment.DevicePath, nil
}

// ExpandReplicatedVolume expands a ReplicatedVolume
func ExpandReplicatedVolume(ctx context.Context, kc client.Client, rv *srv.ReplicatedVolume, newSize resource.Quantity) error {
	rv.Spec.Size = newSize
	return kc.Update(ctx, rv)
}

// BuildReplicatedVolumeSpec builds ReplicatedVolumeSpec from parameters
func BuildReplicatedVolumeSpec(
	size resource.Quantity,
	rscName string,
) srv.ReplicatedVolumeSpec {
	return srv.ReplicatedVolumeSpec{
		Size:                       size,
		ReplicatedStorageClassName: rscName,
		MaxAttachments:             ptr.To(byte(2)),
	}
}

func BuildRVAName(volumeName, nodeName string) string {
	base := "csi-" + volumeName + "-" + nodeName
	if len(base) <= 253 {
		return base
	}

	sum := sha1.Sum([]byte(base))
	hash := hex.EncodeToString(sum[:])[:8]

	// "csi-" + vol + "-" + node + "-" + hash
	const prefixLen = 4 // len("csi-")
	const sepCount = 2  // "-" between parts + "-" before hash
	const hashLen = 8
	maxPartsLen := 253 - prefixLen - sepCount - hashLen
	if maxPartsLen < 2 {
		// Should never happen, but keep a valid, bounded name.
		return "csi-" + hash
	}

	volMax := maxPartsLen / 2
	nodeMax := maxPartsLen - volMax

	volPart := truncateString(volumeName, volMax)
	nodePart := truncateString(nodeName, nodeMax)
	return "csi-" + volPart + "-" + nodePart + "-" + hash
}

func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	// Make the truncation stable and avoid trailing '-' (purely cosmetic, but improves readability).
	out := s[:maxLen]
	out = strings.TrimSuffix(out, "-")
	out = strings.TrimSuffix(out, ".")
	return out
}

func EnsureRVA(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName, nodeName string) (string, error) {
	rvaName := BuildRVAName(volumeName, nodeName)

	existing := &srv.ReplicatedVolumeAttachment{}
	if err := kc.Get(ctx, client.ObjectKey{Name: rvaName}, existing); err == nil {
		// Validate it matches the intended binding.
		if existing.Spec.ReplicatedVolumeName != volumeName || existing.Spec.NodeName != nodeName {
			return "", fmt.Errorf("ReplicatedVolumeAttachment %s already exists but has different spec (volume=%s,node=%s)",
				rvaName, existing.Spec.ReplicatedVolumeName, existing.Spec.NodeName,
			)
		}
		if !slices.Contains(existing.Finalizers, SDSReplicatedVolumeCSIFinalizer) {
			existing.Finalizers = append(existing.Finalizers, SDSReplicatedVolumeCSIFinalizer)
			if err := kc.Update(ctx, existing); err != nil {
				return "", fmt.Errorf("add finalizer to ReplicatedVolumeAttachment %s: %w", rvaName, err)
			}
			log.Info(fmt.Sprintf("[EnsureRVA][traceID:%s][volumeID:%s][node:%s] Added finalizer to existing RVA %s", traceID, volumeName, nodeName, rvaName))
		}
		return rvaName, nil
	} else if client.IgnoreNotFound(err) != nil {
		return "", fmt.Errorf("get ReplicatedVolumeAttachment %s: %w", rvaName, err)
	}

	rva := &srv.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rvaName,
			Finalizers: []string{SDSReplicatedVolumeCSIFinalizer},
		},
		Spec: srv.ReplicatedVolumeAttachmentSpec{
			ReplicatedVolumeName: volumeName,
			NodeName:             nodeName,
		},
	}

	log.Info(fmt.Sprintf("[EnsureRVA][traceID:%s][volumeID:%s][node:%s] Creating ReplicatedVolumeAttachment %s", traceID, volumeName, nodeName, rvaName))
	if err := kc.Create(ctx, rva); err != nil {
		if kerrors.IsAlreadyExists(err) {
			return rvaName, nil
		}
		return "", fmt.Errorf("create ReplicatedVolumeAttachment %s: %w", rvaName, err)
	}
	return rvaName, nil
}

func DeleteRVA(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName, nodeName string) error {
	rvaName := BuildRVAName(volumeName, nodeName)
	rva := &srv.ReplicatedVolumeAttachment{}
	if err := kc.Get(ctx, client.ObjectKey{Name: rvaName}, rva); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info(fmt.Sprintf("[DeleteRVA][traceID:%s][volumeID:%s][node:%s] ReplicatedVolumeAttachment %s not found, skipping", traceID, volumeName, nodeName, rvaName))
			return nil
		}
		return fmt.Errorf("get ReplicatedVolumeAttachment %s: %w", rvaName, err)
	}

	if slices.Contains(rva.Finalizers, SDSReplicatedVolumeCSIFinalizer) {
		rva.Finalizers = slices.DeleteFunc(rva.Finalizers, func(s string) bool { return s == SDSReplicatedVolumeCSIFinalizer })
		if err := kc.Update(ctx, rva); err != nil {
			return fmt.Errorf("remove finalizer from ReplicatedVolumeAttachment %s: %w", rvaName, err)
		}
		log.Info(fmt.Sprintf("[DeleteRVA][traceID:%s][volumeID:%s][node:%s] Removed finalizer from RVA %s", traceID, volumeName, nodeName, rvaName))
	}

	log.Info(fmt.Sprintf("[DeleteRVA][traceID:%s][volumeID:%s][node:%s] Deleting ReplicatedVolumeAttachment %s", traceID, volumeName, nodeName, rvaName))
	if err := kc.Delete(ctx, rva); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// DeleteRVAsForVolume lists all RVAs for the given volume and deletes them.
// This cleans up orphaned RVAs when DeleteVolume is called (e.g. WFFC case where
// ControllerUnpublishVolume was never invoked).
func DeleteRVAsForVolume(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName string) error {
	list := &srv.ReplicatedVolumeAttachmentList{}
	if err := kc.List(ctx, list); err != nil {
		return fmt.Errorf("list ReplicatedVolumeAttachments: %w", err)
	}
	var toDelete []srv.ReplicatedVolumeAttachment
	for _, rva := range list.Items {
		if rva.Spec.ReplicatedVolumeName == volumeName {
			toDelete = append(toDelete, rva)
		}
	}
	for _, rva := range toDelete {
		if slices.Contains(rva.Finalizers, SDSReplicatedVolumeCSIFinalizer) {
			rva.Finalizers = slices.DeleteFunc(rva.Finalizers, func(s string) bool { return s == SDSReplicatedVolumeCSIFinalizer })
			if err := kc.Update(ctx, &rva); err != nil {
				return fmt.Errorf("remove finalizer from ReplicatedVolumeAttachment %s: %w", rva.Name, err)
			}
			log.Info(fmt.Sprintf("[DeleteRVAsForVolume][traceID:%s][volumeID:%s] Removed finalizer from RVA %s", traceID, volumeName, rva.Name))
		}
		log.Info(fmt.Sprintf("[DeleteRVAsForVolume][traceID:%s][volumeID:%s] Deleting ReplicatedVolumeAttachment %s (node=%s)", traceID, volumeName, rva.Name, rva.Spec.NodeName))
		if err := kc.Delete(ctx, &rva); err != nil && !kerrors.IsNotFound(err) {
			return fmt.Errorf("delete ReplicatedVolumeAttachment %s: %w", rva.Name, err)
		}
	}
	if len(toDelete) > 0 {
		log.Info(fmt.Sprintf("[DeleteRVAsForVolume][traceID:%s][volumeID:%s] Deleted %d ReplicatedVolumeAttachment(s)", traceID, volumeName, len(toDelete)))
	}
	return nil
}

// RVAWaitError represents a failure to observe RVA Ready=True.
// It may wrap a context cancellation/deadline error, while still preserving the last seen RVA Ready condition.
type RVAWaitError struct {
	VolumeName string
	NodeName   string
	RVAName    string

	// LastReadyCondition is the last observed Ready condition (may be nil if status/condition was never observed).
	LastReadyCondition *metav1.Condition

	// LastAttachedCondition is the last observed Attached condition (may be nil if missing).
	// This is useful for surfacing detailed attach progress and permanent attach failures.
	LastAttachedCondition *metav1.Condition

	// Permanent indicates that waiting won't help (e.g. locality constraint violation).
	Permanent bool

	// Cause is the underlying error (e.g. context.DeadlineExceeded). May be nil for non-context failures.
	Cause error
}

func (e *RVAWaitError) Unwrap() error { return e.Cause }

func (e *RVAWaitError) Error() string {
	base := fmt.Sprintf("RVA %s for volume=%s node=%s not ready", e.RVAName, e.VolumeName, e.NodeName)
	if e.LastReadyCondition != nil {
		base = fmt.Sprintf("%s: Ready=%s reason=%s message=%q", base, e.LastReadyCondition.Status, e.LastReadyCondition.Reason, e.LastReadyCondition.Message)
	}
	if e.LastAttachedCondition != nil {
		base = fmt.Sprintf("%s: Attached=%s reason=%s message=%q", base, e.LastAttachedCondition.Status, e.LastAttachedCondition.Reason, e.LastAttachedCondition.Message)
	}
	if e.Permanent {
		base += " (permanent)"
	}
	if e.Cause != nil {
		base = fmt.Sprintf("%s: %v", base, e.Cause)
	}
	return base
}

func sleepWithContext(ctx context.Context) error {
	t := time.NewTimer(500 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func WaitForRVAReady(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, volumeName, nodeName string,
) error {
	rvaName := BuildRVAName(volumeName, nodeName)
	var attemptCounter int
	var lastReadyCond *metav1.Condition
	var lastAttachedCond *metav1.Condition
	log.Info(fmt.Sprintf("[WaitForRVAReady][traceID:%s][volumeID:%s][node:%s] Waiting for ReplicatedVolumeAttachment %s to become Ready=True", traceID, volumeName, nodeName, rvaName))
	for {
		attemptCounter++
		if err := ctx.Err(); err != nil {
			log.Warning(fmt.Sprintf("[WaitForRVAReady][traceID:%s][volumeID:%s][node:%s] context done", traceID, volumeName, nodeName))
			return &RVAWaitError{
				VolumeName:            volumeName,
				NodeName:              nodeName,
				RVAName:               rvaName,
				LastReadyCondition:    lastReadyCond,
				LastAttachedCondition: lastAttachedCond,
				Cause:                 err,
			}
		}

		rva := &srv.ReplicatedVolumeAttachment{}
		if err := kc.Get(ctx, client.ObjectKey{Name: rvaName}, rva); err != nil {
			if client.IgnoreNotFound(err) == nil {
				if attemptCounter%10 == 0 {
					log.Info(fmt.Sprintf("[WaitForRVAReady][traceID:%s][volumeID:%s][node:%s] Attempt: %d, RVA not found yet", traceID, volumeName, nodeName, attemptCounter))
				}
				if err := sleepWithContext(ctx); err != nil {
					return &RVAWaitError{
						VolumeName:            volumeName,
						NodeName:              nodeName,
						RVAName:               rvaName,
						LastReadyCondition:    lastReadyCond,
						LastAttachedCondition: lastAttachedCond,
						Cause:                 err,
					}
				}
				continue
			}
			return fmt.Errorf("get ReplicatedVolumeAttachment %s: %w", rvaName, err)
		}

		readyCond := meta.FindStatusCondition(rva.Status.Conditions, srv.ReplicatedVolumeAttachmentCondReadyType)
		attachedCond := meta.FindStatusCondition(rva.Status.Conditions, srv.ReplicatedVolumeAttachmentCondAttachedType)

		if attachedCond != nil {
			attachedCopy := *attachedCond
			lastAttachedCond = &attachedCopy
		}

		if readyCond == nil {
			if attemptCounter%10 == 0 {
				log.Info(fmt.Sprintf("[WaitForRVAReady][traceID:%s][volumeID:%s][node:%s] Attempt: %d, RVA Ready condition missing", traceID, volumeName, nodeName, attemptCounter))
			}
			if err := sleepWithContext(ctx); err != nil {
				return &RVAWaitError{
					VolumeName:            volumeName,
					NodeName:              nodeName,
					RVAName:               rvaName,
					LastReadyCondition:    lastReadyCond,
					LastAttachedCondition: lastAttachedCond,
					Cause:                 err,
				}
			}
			continue
		}

		// Keep a stable copy of the last observed condition for error reporting.
		condCopy := *readyCond
		lastReadyCond = &condCopy

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForRVAReady][traceID:%s][volumeID:%s][node:%s] Attempt: %d, Ready=%s reason=%s message=%q", traceID, volumeName, nodeName, attemptCounter, readyCond.Status, readyCond.Reason, readyCond.Message))
		}

		if readyCond.Status == metav1.ConditionTrue {
			log.Info(fmt.Sprintf("[WaitForRVAReady][traceID:%s][volumeID:%s][node:%s] RVA Ready=True", traceID, volumeName, nodeName))
			return nil
		}

		// Early exit for conditions that will not become Ready without changing the request or topology.
		// Waiting here only burns time and hides the real cause from CSI callers.
		if lastAttachedCond != nil &&
			lastAttachedCond.Status == metav1.ConditionFalse &&
			lastAttachedCond.Reason == srv.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied {
			return &RVAWaitError{
				VolumeName:            volumeName,
				NodeName:              nodeName,
				RVAName:               rvaName,
				LastReadyCondition:    lastReadyCond,
				LastAttachedCondition: lastAttachedCond,
				Permanent:             true,
			}
		}

		if err := sleepWithContext(ctx); err != nil {
			return &RVAWaitError{
				VolumeName:            volumeName,
				NodeName:              nodeName,
				RVAName:               rvaName,
				LastReadyCondition:    lastReadyCond,
				LastAttachedCondition: lastAttachedCond,
				Cause:                 err,
			}
		}
	}
}

// WaitForAttachedToProvided waits for a node name to appear in rv.status.actuallyAttachedTo
func WaitForAttachedToProvided(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, volumeName, nodeName string,
) error {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForAttachedToProvided][traceID:%s][volumeID:%s][node:%s] Waiting for node to appear in status.actuallyAttachedTo", traceID, volumeName, nodeName))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForAttachedToProvided][traceID:%s][volumeID:%s][node:%s] context done", traceID, volumeName, nodeName))
			return ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, volumeName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return fmt.Errorf("ReplicatedVolume %s not found", volumeName)
			}
			return err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForAttachedToProvided][traceID:%s][volumeID:%s][node:%s] Attempt: %d, status.actuallyAttachedTo: %v", traceID, volumeName, nodeName, attemptCounter, rv.Status.ActuallyAttachedTo))
		}

		// Check if node is in status.actuallyAttachedTo
		for _, attachedNode := range rv.Status.ActuallyAttachedTo {
			if attachedNode == nodeName {
				log.Info(fmt.Sprintf("[WaitForAttachedToProvided][traceID:%s][volumeID:%s][node:%s] Node is now in status.actuallyAttachedTo", traceID, volumeName, nodeName))
				return nil
			}
		}

		log.Trace(fmt.Sprintf("[WaitForAttachedToProvided][traceID:%s][volumeID:%s][node:%s] Attempt %d, node not in status.actuallyAttachedTo yet. Waiting...", traceID, volumeName, nodeName, attemptCounter))
	}
}

// WaitForAttachedToRemoved waits for a node name to disappear from rv.status.actuallyAttachedTo
func WaitForAttachedToRemoved(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, volumeName, nodeName string,
) error {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForAttachedToRemoved][traceID:%s][volumeID:%s][node:%s] Waiting for node to disappear from status.actuallyAttachedTo", traceID, volumeName, nodeName))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForAttachedToRemoved][traceID:%s][volumeID:%s][node:%s] context done", traceID, volumeName, nodeName))
			return ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, volumeName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// Volume deleted, consider it as removed
				log.Info(fmt.Sprintf("[WaitForAttachedToRemoved][traceID:%s][volumeID:%s][node:%s] ReplicatedVolume not found, considering node as removed", traceID, volumeName, nodeName))
				return nil
			}
			return err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForAttachedToRemoved][traceID:%s][volumeID:%s][node:%s] Attempt: %d, status.actuallyAttachedTo: %v", traceID, volumeName, nodeName, attemptCounter, rv.Status.ActuallyAttachedTo))
		}

		// Check if node is NOT in status.actuallyAttachedTo
		found := false
		for _, attachedNode := range rv.Status.ActuallyAttachedTo {
			if attachedNode == nodeName {
				found = true
				break
			}
		}

		if !found {
			log.Info(fmt.Sprintf("[WaitForAttachedToRemoved][traceID:%s][volumeID:%s][node:%s] Node is no longer in status.actuallyAttachedTo", traceID, volumeName, nodeName))
			return nil
		}

		log.Trace(fmt.Sprintf("[WaitForAttachedToRemoved][traceID:%s][volumeID:%s][node:%s] Attempt %d, node still in status.actuallyAttachedTo. Waiting...", traceID, volumeName, nodeName, attemptCounter))
	}
}
