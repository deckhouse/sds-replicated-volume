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

// IsReplicatedVolumePastFormation reports whether the RV has completed its
// initial Formation transition. This is the inverse of rv-controller's
// isFormationInProgress predicate.
//
// Monotonicity: DatameshRevision grows monotonically during normal operation
// and is only reset to 0 by reconcileFormationRestartIfTimeoutPassed in the
// rv-controller. That restart path only runs while the RV is already in an
// active Formation transition (i.e. while this predicate is already false).
// Therefore the predicate transitions false -> true exactly once per RV and
// stays true for the remainder of the RV's lifetime. This makes it a safe
// "formation committed" marker for cleanup decisions in CreateVolume.
func IsReplicatedVolumePastFormation(rv *srv.ReplicatedVolume) bool {
	return rv != nil && rv.Status.DatameshRevision > 0 && !hasFormationTransition(rv.Status.DatameshTransitions)
}

// WaitForReplicatedVolumeReady waits for ReplicatedVolume to become ready
// and returns the last-fetched RV on success.
func WaitForReplicatedVolumeReady(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
) (*srv.ReplicatedVolume, int, error) {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Waiting for ReplicatedVolume to become ready", traceID, name))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] context done. Failed to wait for ReplicatedVolume", traceID, name))
			return nil, attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, name)
		if err != nil {
			return nil, attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Attempt: %d, ReplicatedVolume: %+v", traceID, name, attemptCounter, rv))
		}

		if rv.DeletionTimestamp != nil {
			return nil, attemptCounter, fmt.Errorf("failed to create ReplicatedVolume %s, reason: ReplicatedVolume is being deleted", name)
		}

		if IsReplicatedVolumePastFormation(rv) {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] ReplicatedVolume is ready (datameshRevision=%d, no Formation transition)", traceID, name, rv.Status.DatameshRevision))
			return rv, attemptCounter, nil
		}
		log.Trace(fmt.Sprintf("[WaitForReplicatedVolumeReady][traceID:%s][volumeID:%s] Attempt %d, ReplicatedVolume not ready yet (datameshRevision=%d, hasFormation=%v). Waiting...", traceID, name, attemptCounter, rv.Status.DatameshRevision, hasFormationTransition(rv.Status.DatameshTransitions)))
	}
}

// quantityWithBytes formats a resource.Quantity as "106436Ki (109054464 bytes)" for debugging.
func quantityWithBytes(q resource.Quantity) string {
	return fmt.Sprintf("%s (%d bytes)", q.String(), q.Value())
}

// WaitForReplicatedVolumeResized polls until rv.Status.Size >= targetSize
// and returns the last-fetched RV on success.
func WaitForReplicatedVolumeResized(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, name string,
	targetSize resource.Quantity,
) (*srv.ReplicatedVolume, int, error) {
	var attemptCounter int
	log.Info(fmt.Sprintf("[WaitForReplicatedVolumeResized][traceID:%s][volumeID:%s] Waiting for status.size >= %s", traceID, name, quantityWithBytes(targetSize)))
	for {
		attemptCounter++
		select {
		case <-ctx.Done():
			log.Warning(fmt.Sprintf("[WaitForReplicatedVolumeResized][traceID:%s][volumeID:%s] context done after %d attempts", traceID, name, attemptCounter))
			return nil, attemptCounter, ctx.Err()
		default:
			time.Sleep(500 * time.Millisecond)
		}

		rv, err := GetReplicatedVolume(ctx, kc, name)
		if err != nil {
			return nil, attemptCounter, err
		}

		if attemptCounter%10 == 0 {
			var currentSize string
			if rv.Status.Size != nil {
				currentSize = quantityWithBytes(*rv.Status.Size)
			} else {
				currentSize = "<nil>"
			}
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeResized][traceID:%s][volumeID:%s] Attempt: %d, status.size=%s, target=%s", traceID, name, attemptCounter, currentSize, quantityWithBytes(targetSize)))
		}

		if rv.DeletionTimestamp != nil {
			return nil, attemptCounter, fmt.Errorf("ReplicatedVolume %s is being deleted, cannot complete resize", name)
		}

		if rv.Status.Size != nil && rv.Status.Size.Cmp(targetSize) >= 0 {
			log.Info(fmt.Sprintf("[WaitForReplicatedVolumeResized][traceID:%s][volumeID:%s] Resize complete: status.size=%s >= target=%s", traceID, name, quantityWithBytes(*rv.Status.Size), quantityWithBytes(targetSize)))
			return rv, attemptCounter, nil
		}
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

func GetDevicePathFromRVA(ctx context.Context, kc client.Client, volumeName, nodeName string) (string, error) {
	rvaName := BuildRVAName(volumeName, nodeName)
	rva := &srv.ReplicatedVolumeAttachment{}
	if err := kc.Get(ctx, client.ObjectKey{Name: rvaName}, rva); err != nil {
		return "", fmt.Errorf("get ReplicatedVolumeAttachment %s: %w", rvaName, err)
	}
	if rva.Status.DevicePath == "" {
		return "", fmt.Errorf("device path not available in ReplicatedVolumeAttachment %s (phase=%s)", rvaName, rva.Status.Phase)
	}
	return rva.Status.DevicePath, nil
}

// createRVA creates a fresh ReplicatedVolumeAttachment with the CSI finalizer.
// Treats AlreadyExists as success (another caller won the race).
func createRVA(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName, nodeName string) (string, error) {
	rvaName := BuildRVAName(volumeName, nodeName)

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

	log.Info(fmt.Sprintf("[createRVA][traceID:%s][volumeID:%s][node:%s] Creating ReplicatedVolumeAttachment %s", traceID, volumeName, nodeName, rvaName))
	if err := kc.Create(ctx, rva); err != nil {
		if kerrors.IsAlreadyExists(err) {
			return rvaName, nil
		}
		return "", fmt.Errorf("create ReplicatedVolumeAttachment %s: %w", rvaName, err)
	}
	return rvaName, nil
}

func EnsureRVA(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName, nodeName string) (string, error) {
	rvaName := BuildRVAName(volumeName, nodeName)

	existing := &srv.ReplicatedVolumeAttachment{}
	err := kc.Get(ctx, client.ObjectKey{Name: rvaName}, existing)
	if kerrors.IsNotFound(err) {
		return createRVA(ctx, kc, log, traceID, volumeName, nodeName)
	}
	if err != nil {
		return "", fmt.Errorf("get ReplicatedVolumeAttachment %s: %w", rvaName, err)
	}

	if existing.Spec.ReplicatedVolumeName != volumeName || existing.Spec.NodeName != nodeName {
		return "", fmt.Errorf("ReplicatedVolumeAttachment %s already exists but has different spec (volume=%s,node=%s)",
			rvaName, existing.Spec.ReplicatedVolumeName, existing.Spec.NodeName,
		)
	}

	if existing.DeletionTimestamp != nil {
		// Stale RVA from a previous attachment is still being finalized
		// (typically by rv-controller holding RVControllerFinalizer).
		// Wait for it to fully disappear within the parent ctx deadline,
		// then create a fresh one.
		log.Info(fmt.Sprintf("[EnsureRVA][traceID:%s][volumeID:%s][node:%s] Existing RVA %s has DeletionTimestamp (phase=%s, finalizers=%v); waiting for deletion to complete",
			traceID, volumeName, nodeName, rvaName, existing.Status.Phase, existing.Finalizers))
		if werr := WaitForRVADeleted(ctx, kc, log, traceID, volumeName, nodeName); werr != nil {
			return "", fmt.Errorf("wait for stale ReplicatedVolumeAttachment %s deletion: %w", rvaName, werr)
		}
		return createRVA(ctx, kc, log, traceID, volumeName, nodeName)
	}

	if !slices.Contains(existing.Finalizers, SDSReplicatedVolumeCSIFinalizer) {
		existing.Finalizers = append(existing.Finalizers, SDSReplicatedVolumeCSIFinalizer)
		if uerr := kc.Update(ctx, existing); uerr != nil {
			return "", fmt.Errorf("add finalizer to ReplicatedVolumeAttachment %s: %w", rvaName, uerr)
		}
		log.Info(fmt.Sprintf("[EnsureRVA][traceID:%s][volumeID:%s][node:%s] Added finalizer to existing RVA %s", traceID, volumeName, nodeName, rvaName))
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

	if rva.Status.InUse != nil && *rva.Status.InUse {
		return fmt.Errorf("ReplicatedVolumeAttachment %s is still in use on node %s, cannot delete", rvaName, nodeName)
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

// CheckNoRVAsExist lists RVAs for the given volume and returns an error if any
// still exist. This prevents deleting a ReplicatedVolume while it has active
// attachments.
func CheckNoRVAsExist(ctx context.Context, kc client.Client, log *logger.Logger, traceID, volumeName string) error {
	list := &srv.ReplicatedVolumeAttachmentList{}
	if err := kc.List(ctx, list); err != nil {
		return fmt.Errorf("list ReplicatedVolumeAttachments: %w", err)
	}
	var remaining []string
	for _, rva := range list.Items {
		if rva.Spec.ReplicatedVolumeName == volumeName {
			remaining = append(remaining, rva.Name)
		}
	}
	if len(remaining) > 0 {
		return fmt.Errorf("cannot delete volume %s: %d ReplicatedVolumeAttachment(s) still exist: %v", volumeName, len(remaining), remaining)
	}
	log.Info(fmt.Sprintf("[CheckNoRVAsExist][traceID:%s][volumeID:%s] No RVAs found, safe to proceed with deletion", traceID, volumeName))
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

// WaitForRVADeleted polls the ReplicatedVolumeAttachment object until it is
// fully gone from the API (kerrors.IsNotFound). The RVA may still carry the
// rv-controller finalizer after DeleteRVA has been issued; returning OK to
// external-attacher before the RVA is fully deleted opens a race window where
// the next ControllerPublishVolume on the same (volume,node) pair observes a
// stale RVA with DeletionTimestamp and fails.
func WaitForRVADeleted(
	ctx context.Context,
	kc client.Client,
	log *logger.Logger,
	traceID, volumeName, nodeName string,
) error {
	rvaName := BuildRVAName(volumeName, nodeName)
	var attemptCounter int
	var lastFinalizers []string
	var lastPhase string
	log.Info(fmt.Sprintf("[WaitForRVADeleted][traceID:%s][volumeID:%s][node:%s] Waiting for ReplicatedVolumeAttachment %s to be fully deleted", traceID, volumeName, nodeName, rvaName))
	for {
		attemptCounter++
		if err := ctx.Err(); err != nil {
			log.Warning(fmt.Sprintf("[WaitForRVADeleted][traceID:%s][volumeID:%s][node:%s] context done after %d attempts; last finalizers=%v phase=%q: %v", traceID, volumeName, nodeName, attemptCounter, lastFinalizers, lastPhase, err))
			return fmt.Errorf("wait for RVA %s deletion: last finalizers=%v phase=%q: %w", rvaName, lastFinalizers, lastPhase, err)
		}

		rva := &srv.ReplicatedVolumeAttachment{}
		err := kc.Get(ctx, client.ObjectKey{Name: rvaName}, rva)
		if err != nil {
			if kerrors.IsNotFound(err) {
				log.Info(fmt.Sprintf("[WaitForRVADeleted][traceID:%s][volumeID:%s][node:%s] ReplicatedVolumeAttachment %s is fully deleted", traceID, volumeName, nodeName, rvaName))
				return nil
			}
			return fmt.Errorf("get ReplicatedVolumeAttachment %s: %w", rvaName, err)
		}

		lastFinalizers = append(lastFinalizers[:0], rva.Finalizers...)
		lastPhase = string(rva.Status.Phase)

		if attemptCounter%10 == 0 {
			log.Info(fmt.Sprintf("[WaitForRVADeleted][traceID:%s][volumeID:%s][node:%s] Attempt: %d, still present (deletionTimestamp=%v finalizers=%v phase=%q)", traceID, volumeName, nodeName, attemptCounter, rva.DeletionTimestamp, lastFinalizers, lastPhase))
		}

		if err := sleepWithContext(ctx); err != nil {
			log.Warning(fmt.Sprintf("[WaitForRVADeleted][traceID:%s][volumeID:%s][node:%s] context done while sleeping after %d attempts; last finalizers=%v phase=%q: %v", traceID, volumeName, nodeName, attemptCounter, lastFinalizers, lastPhase, err))
			return fmt.Errorf("wait for RVA %s deletion: last finalizers=%v phase=%q: %w", rvaName, lastFinalizers, lastPhase, err)
		}
	}
}

