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

package rvpublishcontroller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	commonapi "github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

var _ reconcile.Reconciler = &Reconciler{}

const (
	ConditionTypePublishSucceeded          = "PublishSucceeded"
	ReasonUnableToProvideLocalVolumeAccess = "UnableToProvideLocalVolumeAccess"
	DefaultRetiresCount                    = 5
	DefaultRetryWaitInterval               = 5
)

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	// fetch target ReplicatedVolume; if it was deleted, stop reconciliation
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: req.Name}, rv); err != nil {
		log.Error(err, "unable to get ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// check basic preconditions from spec before doing any work
	if shouldSkipRV(rv, log) {
		return reconcile.Result{}, nil
	}

	// load ReplicatedStorageClass and all replicas of this RV
	rsc, replicasForRV, err := r.loadPublishContext(ctx, rv, log)
	if err != nil {
		return reconcile.Result{}, err
	}

	// validate local access constraints for volumeAccess=Local; may set PublishSucceeded=False and stop
	if err := r.checkIfLocalAccessHasEnoughDiskfulReplicas(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	// sync rv.status.drbd.config.allowTwoPrimaries and, when needed, wait until it is actually applied on replicas
	if err := r.syncAllowTwoPrimaries(ctx, rv, log); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.waitForAllowTwoPrimariesApplied(ctx, rv, log); err != nil {
		return reconcile.Result{}, err
	}

	// sync primary roles on replicas and rv.status.publishedOn
	if err := r.syncReplicaPrimariesAndPublishedOn(ctx, rv, rsc, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// loadPublishContext fetches ReplicatedStorageClass and all non-deleted replicas
// for the given ReplicatedVolume. It returns data needed for publish logic.
func (r *Reconciler) loadPublishContext(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (*v1alpha1.ReplicatedStorageClass, []v1alpha3.ReplicatedVolumeReplica, error) {
	// read ReplicatedStorageClass to understand volumeAccess and other policies
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "unable to get ReplicatedStorageClass")
		return nil, nil, client.IgnoreNotFound(err)
	}

	// list all ReplicatedVolumeReplica objects and filter those that belong to this RV
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return nil, nil, err
	}

	var replicasForRV []v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range rvrList.Items {
		// select replicas of this volume that are not marked for deletion
		if rvr.Spec.ReplicatedVolumeName == rv.Name && rvr.DeletionTimestamp.IsZero() {
			replicasForRV = append(replicasForRV, rvr)
		}
	}

	return rsc, replicasForRV, nil
}

// checkIfLocalAccessHasEnoughDiskfulReplicas enforces the rule that for volumeAccess=Local there must be
// a Diskful replica on each node from rv.spec.publishOn. On violation it sets
// PublishSucceeded=False and stops reconciliation.
func (r *Reconciler) checkIfLocalAccessHasEnoughDiskfulReplicas(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRVList []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// this validation is relevant only when volumeAccess is Local
	if rsc.Spec.VolumeAccess != "Local" {
		return nil
	}

	// map replicas by NodeName for efficient lookup
	NodeNameToRvrMap := make(map[string]*v1alpha3.ReplicatedVolumeReplica, len(replicasForRVList))
	for _, rvr := range replicasForRVList {
		NodeNameToRvrMap[rvr.Spec.NodeName] = &rvr
	}

	// In case rsc.spec.volumeAccess==Local, but replica is not Diskful or doesn't exist,
	// promotion is impossible: update PublishSucceeded on RV and stop reconcile.
	for _, publishNodeName := range rv.Spec.PublishOn {
		rvr, ok := NodeNameToRvrMap[publishNodeName]
		if !ok || rvr.Spec.Type != "Diskful" {
			if err := commonapi.PatchStatusWithConflictRetry(ctx, r.cl, rv, func(patchedRV *v1alpha3.ReplicatedVolume) error {
				if patchedRV.Status == nil {
					patchedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
				}
				meta.SetStatusCondition(&patchedRV.Status.Conditions, metav1.Condition{
					Type:    ConditionTypePublishSucceeded,
					Status:  metav1.ConditionFalse,
					Reason:  ReasonUnableToProvideLocalVolumeAccess,
					Message: fmt.Sprintf("Local access required but no Diskful replica found on node %s", publishNodeName),
				})
				return nil
			}); err != nil {
				log.Error(err, "unable to update ReplicatedVolume PublishSucceeded=False")
				return err
			}

			// stop reconciliation after setting the failure condition
			return nil
		}
	}

	return nil
}

// syncAllowTwoPrimaries updates rv.status.drbd.config.allowTwoPrimaries according to
// the number of nodes in rv.spec.publishOn. Waiting for actual application on
// replicas is handled separately by waitForAllowTwoPrimariesApplied.
func (r *Reconciler) syncAllowTwoPrimaries(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) error {
	// update rv.status.drbd.config.allowTwoPrimaries according to spec:
	// - true when exactly 2 nodes are requested in spec.publishOn
	// - false otherwise
	desiredAllowTwoPrimaries := len(rv.Spec.PublishOn) == 2
	if err := commonapi.PatchStatusWithConflictRetry(ctx, r.cl, rv, func(patchedRV *v1alpha3.ReplicatedVolume) error {
		if patchedRV.Status == nil {
			patchedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if patchedRV.Status.DRBD == nil {
			patchedRV.Status.DRBD = &v1alpha3.DRBDResource{}
		}
		if patchedRV.Status.DRBD.Config == nil {
			patchedRV.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
		}
		patchedRV.Status.DRBD.Config.AllowTwoPrimaries = desiredAllowTwoPrimaries
		return nil
	}); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolume allowTwoPrimaries")
			return err
		}

		// RV was deleted concurrently; nothing left to publish for
		return nil
	}

	return nil
}

func (r *Reconciler) waitForAllowTwoPrimariesApplied(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) error {
	// when two nodes are requested in publishOn, we must wait until
	// rvr.status.drbd.actual.allowTwoPrimaries is applied on all replicas
	if len(rv.Spec.PublishOn) != 2 {
		return nil
	}

	var maxAttempts = DefaultRetiresCount
	for attempt := 0; attempt < DefaultRetiresCount; attempt++ {
		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		if err := r.cl.List(ctx, rvrList); err != nil {
			log.Error(err, "unable to list ReplicatedVolumeReplica while waiting for allowTwoPrimaries", "attempt", attempt+1)
			return err
		}

		allReady := true
		for _, rvr := range rvrList.Items {
			if rvr.Spec.ReplicatedVolumeName != rv.Name || !rvr.DeletionTimestamp.IsZero() {
				continue
			}

			if rvr.Status == nil ||
				rvr.Status.DRBD == nil ||
				rvr.Status.DRBD.Actual == nil ||
				!rvr.Status.DRBD.Actual.AllowTwoPrimaries {
				allReady = false
				log.Info("waiting for allowTwoPrimaries to be applied on all replicas", "rvr", rvr.Name, "attempt", attempt+1)
				break
			}
		}

		if allReady {
			return nil
		}

		if attempt < maxAttempts-1 {
			time.Sleep(DefaultRetryWaitInterval * time.Second)
		}
	}

	log.Error(errors.New("some RVR has not been switched to allowTwoPrimaries yet"), "allowTwoPrimaries not applied on all replicas after retries")
	return errors.New("some RVR has not been switched to allowTwoPrimaries yet")
}

// syncReplicaPrimariesAndPublishedOn updates rvr.status.drbd.config.primary (and spec.type for TieBreaker)
// for all replicas according to rv.spec.publishOn and recomputes rv.status.publishedOn
// from actual DRBD roles on replicas.
func (r *Reconciler) syncReplicaPrimariesAndPublishedOn(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass, // kept for future use if topology/volumeAccess will affect primary placement
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// desired primary set: replicas on nodes from rv.spec.publishOn should be primary
	publishSet := make(map[string]struct{}, len(rv.Spec.PublishOn))
	for _, nodeName := range rv.Spec.PublishOn {
		publishSet[nodeName] = struct{}{}
	}

	for _, rvr := range replicasForRV {
		if rvr.Spec.NodeName == "" {
			continue
		}

		_, shouldBePrimary := publishSet[rvr.Spec.NodeName]

		// update both spec.type (for TieBreaker) and rvr.status.drbd.config.primary in a single patch as required by spec
		if err := commonapi.PatchWithConflictRetry(ctx, r.cl, &rvr, func(patchedRVR *v1alpha3.ReplicatedVolumeReplica) error {
			// convert TieBreaker to Access when it is supposed to be Primary
			if shouldBePrimary && patchedRVR.Spec.Type == "TieBreaker" {
				patchedRVR.Spec.Type = "Access"
			}

			if patchedRVR.Status == nil {
				patchedRVR.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
			}
			if patchedRVR.Status.DRBD == nil {
				patchedRVR.Status.DRBD = &v1alpha3.DRBD{}
			}
			if patchedRVR.Status.DRBD.Config == nil {
				patchedRVR.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
			}

			current := false
			if patchedRVR.Status.DRBD.Config.Primary != nil {
				current = *patchedRVR.Status.DRBD.Config.Primary
			}
			if current == shouldBePrimary {
				return nil
			}

			patchedRVR.Status.DRBD.Config.Primary = &shouldBePrimary
			return nil
		}); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "unable to patch ReplicatedVolumeReplica primary", "rvr", rvr.Name)
				return err
			}
			// replica was deleted concurrently; continue with the rest
		}
	}

	// recompute rv.status.publishedOn from actual DRBD roles on replicas
	publishedOn := make([]string, 0, len(replicasForRV))
	for _, rvr := range replicasForRV {
		if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
			continue
		}
		if rvr.Status.DRBD.Status.Role != "Primary" {
			continue
		}
		if rvr.Spec.NodeName == "" {
			continue
		}
		publishedOn = append(publishedOn, rvr.Spec.NodeName)
	}

	// apply publishedOn via status patch with conflict retry
	if err := commonapi.PatchStatusWithConflictRetry(ctx, r.cl, rv, func(patchedRV *v1alpha3.ReplicatedVolume) error {
		if patchedRV.Status == nil {
			patchedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		patchedRV.Status.PublishedOn = publishedOn
		return nil
	}); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolume publishedOn")
			return err
		}
		// RV was deleted concurrently; nothing left to publish for
	}

	return nil
}

// shouldSkipRV returns true when, according to spec, rv-publish-controller
// should not perform any actions for the given ReplicatedVolume.
func shouldSkipRV(rv *v1alpha3.ReplicatedVolume, log logr.Logger) bool {
	// controller works only when status is initialized
	if rv.Status == nil {
		return true
	}

	// controller works only when RV is Ready according to spec
	if !meta.IsStatusConditionTrue(rv.Status.Conditions, "Ready") {
		return true
	}

	// fetch ReplicatedStorageClass to inspect volumeAccess mode and other policies
	if rv.Spec.ReplicatedStorageClassName == "" {
		log.Info("ReplicatedStorageClassName is empty, skipping")
		return true
	}

	return false
}
