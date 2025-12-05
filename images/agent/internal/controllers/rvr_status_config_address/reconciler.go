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
	"errors"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/config"
	v1 "k8s.io/api/core/v1"
)

var ErrNoPortsAvailable = errors.New("no free port available")

type Reconciler struct {
	cl      client.Client
	log     logr.Logger
	drbdCfg config.DRBDConfig
}

var _ reconcile.Reconciler = &Reconciler{}

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, log logr.Logger, drbdCfg config.DRBDConfig) *Reconciler {
	if drbdCfg.MinPort == 0 {
		panic("Minimal DRBD port can't be 0 to be able to distinguish the port unset case")
	}
	return &Reconciler{
		cl:      cl,
		log:     log,
		drbdCfg: drbdCfg,
	}
}

// Reconcile reconciles a Node to configure addresses for all ReplicatedVolumeReplicas on that node.
// We reconcile the Node (not individual RVRs) to avoid race conditions when finding free ports.
// This approach allows us to process all RVRs on a node atomically in a single reconciliation loop.
// Note: This logic could be moved from the agent to the controller in the future if needed.
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", request)
	log.Info("Reconcile start")

	var node v1.Node
	if err := r.cl.Get(ctx, request.NamespacedName, &node); err != nil {
		log.Error(err, "Can't get Node")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Extract InternalIP
	nodeAddressIndex := slices.IndexFunc(node.Status.Addresses, func(address v1.NodeAddress) bool {
		return address.Type == v1.NodeInternalIP
	})
	if nodeAddressIndex < 0 {
		log.Error(ErrNodeMissingInternalIP, "Node don't have InternalIP address. Returning error to reconcile later")
		return reconcile.Result{}, fmt.Errorf("%w: %s", ErrNodeMissingInternalIP, node.Name)
	}
	nodeInternalIP := node.Status.Addresses[nodeAddressIndex].Address

	// List all RVRs on this node that need address configuration
	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &rvrList); err != nil {
		log.Error(err, "Can't list ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	// Keep only RVR on that node
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return rvr.Spec.NodeName != node.Name
	})

	// Instantiate the Address field here to simplify code. Zero port means not set
	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		if rvr.Status == nil {
			rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if rvr.Status.DRBD == nil {
			rvr.Status.DRBD = &v1alpha3.DRBD{}
		}
		if rvr.Status.DRBD.Config == nil {
			rvr.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
		}
		if rvr.Status.DRBD.Config.Address == nil {
			rvr.Status.DRBD.Config.Address = &v1alpha3.Address{}
		}
	}

	// Build map of used ports from all RVRs removing the RVR with valid port and the not changed IPv4
	usedPorts := make(map[uint]struct{})
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		if !r.drbdCfg.IsPortValid(rvr.Status.DRBD.Config.Address.Port) {
			return false // keep invalid
		}
		// mark as used
		usedPorts[rvr.Status.DRBD.Config.Address.Port] = struct{}{}

		// delete only rvr with same address
		return nodeInternalIP == rvr.Status.DRBD.Config.Address.IPv4
	})

	// Process each RVR that needs address configuration
	for _, rvr := range rvrList.Items {
		log := log.WithValues("rvr", rvr.Name)

		// Create a patch from the current state at the beginning
		patch := client.MergeFrom(rvr.DeepCopy())

		// If no valid existing port, find the smallest free port in the range
		var portToAssign uint = rvr.Status.DRBD.Config.Address.Port

		// Change port only if it's invalid
		if !r.drbdCfg.IsPortValid(portToAssign) {
			for port := r.drbdCfg.MinPort; port <= r.drbdCfg.MaxPort; port++ {
				if _, used := usedPorts[port]; !used {
					portToAssign = port
					usedPorts[portToAssign] = struct{}{} // Mark as used for next RVR
					break
				}
			}
		}

		if portToAssign == 0 {
			log.Error(ErrNoPortsAvailable, "Out of free ports", "minPort", r.drbdCfg.MinPort, "maxPort", r.drbdCfg.MaxPort)
			if changed := r.setCondition(
				&rvr,
				metav1.ConditionFalse,
				v1alpha3.ReasonNoFreePortAvailable,
				"No free port available",
			); changed {
				if err := r.cl.Status().Patch(ctx, &rvr, patch); err != nil {
					log.Error(err, "Failed to patch status")
					return reconcile.Result{}, err
				}
			}
			continue // process next rvr
		}

		// Set address and condition
		address := &v1alpha3.Address{
			IPv4: nodeInternalIP,
			Port: portToAssign,
		}
		log = log.WithValues("address", address)

		// Patch status once at the end if anything changed
		if changed := r.setAddressAndCondition(&rvr, address); changed {
			if err := r.cl.Status().Patch(ctx, &rvr, patch); err != nil {
				log.Error(err, "Failed to patch status")
				return reconcile.Result{}, err
			}
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
	conditionChanged := r.setCondition(
		rvr,
		metav1.ConditionTrue,
		v1alpha3.ReasonAddressConfigurationSucceeded,
		"Address configured",
	)

	return addressChanged || conditionChanged
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
