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

package rvrstatusconfigaddress

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/cluster"
)

type Reconciler struct {
	cl      client.Client
	log     logr.Logger
	drbdCfg DRBDConfig
}

type DRBDConfig interface {
	DRBDMinPort() uint
	DRBDMaxPort() uint
}

func IsPortValid(c DRBDConfig, port uint) bool {
	return port >= c.DRBDMinPort() && port <= c.DRBDMaxPort()
}

var _ reconcile.Reconciler = &Reconciler{}

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

// Reconcile reconciles a Node to configure addresses for all ReplicatedVolumeReplicas on that node.
// We reconcile the Node (not individual RVRs) to avoid race conditions when finding free ports.
// This approach allows us to process all RVRs on a node atomically in a single reconciliation loop.
// Note: This logic could be moved from the agent to the controller in the future if needed.
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", request)
	log.Info("Reconcile start")

	// Get Node to extract InternalIP
	var node corev1.Node
	if err := r.cl.Get(ctx, request.NamespacedName, &node); err != nil {
		log.Error(err, "Can't get Node")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Extract InternalIP from node
	nodeIP, err := getInternalIP(&node)
	if err != nil {
		log.Error(err, "Node missing InternalIP")
		return reconcile.Result{}, err
	}

	// Get DRBD port settings
	settings, err := cluster.GetSettings(ctx, r.cl)
	if err != nil {
		log.Error(err, "Can't get DRBD port settings")
		return reconcile.Result{}, fmt.Errorf("%w: %w", ErrConfigSettings, err)
	}

	// List all RVRs on this node that need address configuration
	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &rvrList); err != nil {
		log.Error(err, "Can't list ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	// Just in case if MatchingFilterSelector is not working as expected
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return rvr.Spec.NodeName != node.Name
	})

	// Build map of used ports from all RVRs on this node
	usedPorts := make(map[uint]struct{})
	for _, rvr := range rvrList.Items {
		if rvr.Status != nil &&
			rvr.Status.DRBD != nil &&
			rvr.Status.DRBD.Config != nil &&
			rvr.Status.DRBD.Config.Address != nil {
			usedPorts[rvr.Status.DRBD.Config.Address.Port] = struct{}{}
		}
	}

	// Process each RVR that needs address configuration
	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]

		log := log.WithValues("rvr", rvr.Name)
		// Create a patch from the current state at the beginning
		patch := client.MergeFrom(rvr.DeepCopy())

		// Check if RVR already has a valid port that we can reuse
		var freePort uint
		found := false
		if rvr.Status != nil &&
			rvr.Status.DRBD != nil &&
			rvr.Status.DRBD.Config != nil &&
			rvr.Status.DRBD.Config.Address != nil {
			existingPort := rvr.Status.DRBD.Config.Address.Port
			// Check if existing port is in valid range
			if existingPort >= settings.DRBDMinPort &&
				existingPort <= settings.DRBDMaxPort &&
				existingPort != 0 {
				freePort = existingPort
				found = true
				// Port is already in usedPorts from initial build, no need to add again
			}
		}

		// If no valid existing port, find the smallest free port in the range
		if !found {
			for port := settings.DRBDMinPort; port <= settings.DRBDMaxPort; port++ {
				if _, used := usedPorts[port]; !used {
					freePort = port
					found = true
					usedPorts[port] = struct{}{} // Mark as used for next RVR
					break
				}
			}
		}

		if !found {
			log.Error(
				fmt.Errorf("no free port available in range [%d, %d]",
					settings.DRBDMinPort, settings.DRBDMaxPort,
				),
				"No free port available",
			)

			if !r.setCondition(rvr, metav1.ConditionFalse, v1alpha3.ReasonNoFreePortAvailable, "No free port available") {
				continue
			}

			if err := r.cl.Status().Patch(ctx, rvr, patch); err != nil {
				log.Error(err, "Failed to patch status")
				return reconcile.Result{}, err
			}
			continue
		}

		// Set address and condition
		address := &v1alpha3.Address{
			IPv4: nodeIP,
			Port: freePort,
		}
		log = log.WithValues("address", address)

		// Patch status once at the end if anything changed
		if !r.setAddressAndCondition(rvr, address) {
			continue
		}

		if err := r.cl.Status().Patch(ctx, rvr, patch); err != nil {
			log.Error(err, "Failed to patch status")
			return reconcile.Result{}, err
		}

		log.Info("Address configured")
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) setAddressAndCondition(rvr *v1alpha3.ReplicatedVolumeReplica, address *v1alpha3.Address) bool {
	// Check if address is already set correctly
	addressChanged := rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil ||
		rvr.Status.DRBD.Config.Address == nil || *rvr.Status.DRBD.Config.Address != *address

	// Apply address changes if needed
	if addressChanged {
		if rvr.Status == nil {
			rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if rvr.Status.DRBD == nil {
			rvr.Status.DRBD = &v1alpha3.DRBD{}
		}
		if rvr.Status.DRBD.Config == nil {
			rvr.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
		}
		rvr.Status.DRBD.Config.Address = address
	}

	// Set condition using helper function (it checks if condition needs to be updated)
	condChanged := r.setCondition(
		rvr,
		metav1.ConditionTrue,
		v1alpha3.ReasonAddressConfigurationSucceeded,
		"Address configured",
	)

	return addressChanged || condChanged
}

func (r *Reconciler) setCondition(rvr *v1alpha3.ReplicatedVolumeReplica, status metav1.ConditionStatus, reason, message string) bool {
	// Check if condition is already set correctly
	if rvr.Status != nil && rvr.Status.Conditions != nil {
		cond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeAddressConfigured)
		if cond != nil &&
			cond.Status == status &&
			cond.Reason == reason &&
			cond.Message == message {
			// Already set correctly, no need to patch
			return false
		}
	}

	// Apply changes
	if rvr.Status == nil {
		rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}
	if rvr.Status.Conditions == nil {
		rvr.Status.Conditions = []metav1.Condition{}
	}

	meta.SetStatusCondition(
		&rvr.Status.Conditions,
		metav1.Condition{
			Type:    v1alpha3.ConditionTypeAddressConfigured,
			Status:  status,
			Reason:  reason,
			Message: message,
		},
	)

	return true
}
