package rvr

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	. "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/common/lang"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const rvrFinalizerName = "sds-replicated-volume.deckhouse.io/agent"

type resourceReconcileRequestHandler struct {
	ctx      context.Context
	log      *slog.Logger
	cl       client.Client
	nodeName string
	cfg      *ReconcilerClusterConfig
	rvr      *v1alpha2.ReplicatedVolumeReplica
}

func (h *resourceReconcileRequestHandler) Handle() error {
	// validate
	diskless, err := h.rvr.Diskless()
	if err != nil {
		return err
	}

	// ensure finalizer present during normal reconcile
	err = api.PatchWithConflictRetry(
		h.ctx, h.cl, h.rvr,
		func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
			if slices.Contains(rvr.Finalizers, rvrFinalizerName) {
				return nil
			}
			rvr.Finalizers = append(rvr.Finalizers, rvrFinalizerName)
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("ensuring finalizer: %w", err)
	}

	if err := h.writeResourceConfig(); err != nil {
		return h.failAdjustmentWithReason(
			"failed to write resource config",
			err,
			v1alpha2.ReasonConfigurationFailed,
		)
	}

	if !diskless {
		exists, err := drbdadm.ExecuteDumpMD_MetadataExists(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
		if err != nil {
			return h.failAdjustmentWithReason(
				"failed to check metadata existence",
				err,
				v1alpha2.ReasonMetadataCheckFailed,
			)
		}

		var transitionToSafeForInitialSync bool
		if !exists {
			if err := h.setConditionIfNeeded(
				v1alpha2.ConditionTypeInitialSync,
				metav1.ConditionFalse,
				v1alpha2.ReasonInitialSyncRequiredButNotReady,
				"Creating metadata needed for initial sync",
				h.rvr.Generation,
			); err != nil {
				return err
			}

			if err := drbdadm.ExecuteCreateMD(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
				return h.failAdjustmentWithReason(
					"failed to create metadata",
					err,
					v1alpha2.ReasonMetadataCreationFailed,
				)
			}
			h.log.Info("successfully created metadata")

			transitionToSafeForInitialSync = true
		} else {
			initialSyncCond := meta.FindStatusCondition(h.rvr.Status.Conditions, v1alpha2.ConditionTypeInitialSync)
			if initialSyncCond != nil && initialSyncCond.Reason == v1alpha2.ReasonInitialSyncRequiredButNotReady {
				h.log.Warn("metadata has been created, but status condition is not updated, fixing")
				transitionToSafeForInitialSync = true
			}
		}

		if transitionToSafeForInitialSync {
			if err := h.setConditionIfNeeded(
				v1alpha2.ConditionTypeInitialSync,
				metav1.ConditionFalse,
				v1alpha2.ReasonSafeForInitialSync,
				"Safe for initial synchronization",
				h.rvr.Generation,
			); err != nil {
				return err
			}
			h.log.Debug("transitioned to " + v1alpha2.ReasonSafeForInitialSync)
		}
	}

	isUp, err := drbdadm.ExecuteStatus_IsUp(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return h.failAdjustmentWithReason(
			"failed to check resource status",
			err,
			v1alpha2.ReasonStatusCheckFailed,
		)
	}

	if !isUp {
		if err := drbdadm.ExecuteUp(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			return h.failAdjustmentWithReason(
				"failed to bring up resource",
				err,
				v1alpha2.ReasonResourceUpFailed,
			)
		}

		h.log.Info("successfully brought up resource")
	}

	if err := drbdadm.ExecuteAdjust(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		return h.failAdjustmentWithReason(
			"failed to adjust resource",
			err,
			v1alpha2.ReasonAdjustmentFailed,
		)
	}

	h.log.Info("successfully adjusted resource")

	if err := h.setConditionIfNeeded(
		v1alpha2.ConditionTypeConfigurationAdjusted,
		metav1.ConditionTrue,
		v1alpha2.ReasonAdjustmentSucceeded,
		"Replica is configured",
		h.rvr.Generation,
	); err != nil {
		return err
	}

	if err := h.handlePrimarySecondary(); err != nil {
		return fmt.Errorf("handling primary/secondary: %w", err)
	}

	return nil
}

func (h *resourceReconcileRequestHandler) writeResourceConfig() error {
	resourceCfg := h.generateResourceConfig()

	rootSection := &drbdconf.Section{}

	if err := drbdconf.Marshal(resourceCfg, rootSection); err != nil {
		return fmt.Errorf("marshaling resource %s cfg: %w", h.rvr.Spec.ReplicatedVolumeName, err)
	}

	root := &drbdconf.Root{}

	for _, sec := range rootSection.Elements {
		root.Elements = append(root.Elements, sec.(*drbdconf.Section))
	}

	filepath := filepath.Join(resourcesDir, h.rvr.Spec.ReplicatedVolumeName+".res")

	file, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open file %s: %w", filepath, err)
	}

	defer file.Close()

	n, err := root.WriteTo(file)
	if err != nil {
		return fmt.Errorf("writing file %s: %w", filepath, err)
	}

	h.log.Info("successfully wrote 'n' bytes to 'file'", "n", n, "file", filepath)
	return nil
}

func (h *resourceReconcileRequestHandler) generateResourceConfig() *v9.Config {
	res := &v9.Resource{
		Name: h.rvr.Spec.ReplicatedVolumeName,
		Net: &v9.Net{
			Protocol:          v9.ProtocolC,
			SharedSecret:      h.rvr.Spec.SharedSecret,
			RRConflict:        v9.RRConflictPolicyRetryConnect,
			AllowTwoPrimaries: h.rvr.Spec.AllowTwoPrimaries,
		},
		Options: &v9.Options{
			OnNoQuorum:                 v9.OnNoQuorumPolicySuspendIO,
			OnNoDataAccessible:         v9.OnNoDataAccessiblePolicySuspendIO,
			OnSuspendedPrimaryOutdated: v9.OnSuspendedPrimaryOutdatedPolicyForceSecondary,
		},
	}

	res.Options.Quorum = If[v9.Quorum](
		h.rvr.Spec.Quorum == 0,
		&v9.QuorumOff{},
		&v9.QuorumNumeric{
			Value: int(h.rvr.Spec.Quorum),
		},
	)

	res.Options.QuorumMinimumRedundancy = If[v9.QuorumMinimumRedundancy](
		h.rvr.Spec.QuorumMinimumRedundancy == 0,
		&v9.QuorumMinimumRedundancyOff{},
		&v9.QuorumMinimumRedundancyNumeric{
			Value: int(h.rvr.Spec.QuorumMinimumRedundancy),
		},
	)

	// current node
	h.populateResourceForNode(res, h.nodeName, h.rvr.Spec.NodeId, h.rvr.Spec.NodeAddress, nil)

	// peers
	for peerName, peer := range h.rvr.Spec.Peers {
		if peerName == h.nodeName {
			h.log.Warn("Current node appeared in a peer list. Ignored.")
			continue
		}
		h.populateResourceForNode(res, peerName, peer.NodeId, peer.Address, &peer)
	}

	return &v9.Config{
		Resources: []*v9.Resource{res},
	}
}

func (h *resourceReconcileRequestHandler) populateResourceForNode(
	res *v9.Resource,
	nodeName string, nodeId uint, nodeAddress v1alpha2.Address,
	peerOptions *v1alpha2.Peer, // nil for current node
) {
	isCurrentNode := nodeName == h.nodeName

	onSection := &v9.On{
		HostNames: []string{nodeName},
		NodeId:    Ptr(nodeId),
	}

	// volumes
	for _, volume := range h.rvr.Spec.Volumes {
		vol := &v9.Volume{
			Number:   Ptr(int(volume.Number)),
			Device:   Ptr(v9.DeviceMinorNumber(volume.Device)),
			MetaDisk: &v9.VolumeMetaDiskInternal{},
		}

		// some information is node-specific, so skip for other nodes
		if isCurrentNode {
			if volume.Disk == "" {
				vol.Disk = &v9.VolumeDiskNone{}
			} else {
				vol.Disk = Ptr(v9.VolumeDisk(volume.Disk))
			}
			vol.DiskOptions = &v9.DiskOptions{
				DiscardZeroesIfAligned: Ptr(false),
				RsDiscardGranularity:   Ptr(uint(8192)),
			}
		} else {
			if !peerOptions.Diskless {
				vol.Disk = Ptr(v9.VolumeDisk("/not/used"))
			}
		}
		onSection.Volumes = append(onSection.Volumes, vol)
	}

	res.On = append(res.On, onSection)

	// connections
	if !isCurrentNode {
		con := &v9.Connection{
			Hosts: []v9.HostAddress{
				apiAddressToV9HostAddress(h.nodeName, h.rvr.Spec.NodeAddress),
				apiAddressToV9HostAddress(nodeName, nodeAddress),
			},
		}

		if peerOptions.SharedSecret != "" {
			con.Net = &v9.Net{
				SharedSecret: peerOptions.SharedSecret,
			}
		}

		res.Connections = append(res.Connections, con)
	}
}

func apiAddressToV9HostAddress(hostname string, address v1alpha2.Address) v9.HostAddress {
	return v9.HostAddress{
		Name:            hostname,
		AddressWithPort: fmt.Sprintf("%s:%d", address.IPv4, address.Port),
		AddressFamily:   "ipv4",
	}
}

func (h *resourceReconcileRequestHandler) handlePrimarySecondary() error {
	if !meta.IsStatusConditionTrue(h.rvr.Status.Conditions, v1alpha2.ConditionTypeInitialSync) {
		h.log.Debug(
			"initial synchronization has not been completed, skipping primary/secondary promotion",
			"conditions", h.rvr.Status.Conditions,
		)
		return nil
	}

	statusResult, err := drbdsetup.ExecuteStatus(h.ctx)
	if err != nil {
		h.log.Error("failed to get DRBD status", "error", err)
		return fmt.Errorf("getting DRBD status: %w", err)
	}

	var currentRole string
	for _, resource := range statusResult {
		if resource.Name == h.rvr.Spec.ReplicatedVolumeName {
			currentRole = resource.Role
			break
		}
	}

	if currentRole == "" {
		h.log.Error("resource not found in DRBD status")
		return fmt.Errorf("resource %s not found in DRBD status", h.rvr.Spec.ReplicatedVolumeName)
	}

	desiredRole := "Secondary"
	if h.rvr.Spec.Primary {
		desiredRole = "Primary"
	}

	if currentRole == desiredRole {
		h.log.Debug("DRBD role already correct", "role", currentRole)
		return nil
	}

	if h.rvr.Spec.Primary {
		err := drbdadm.ExecutePrimary(h.ctx, h.rvr.Spec.ReplicatedVolumeName)

		if err != nil {
			h.log.Error("failed to promote to primary", "error", err)
			return fmt.Errorf("promoting to primary: %w", err)
		}

		h.log.Info("successfully promoted to primary")
	} else {
		if err := drbdadm.ExecuteSecondary(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			h.log.Error("failed to demote to secondary", "error", err)
			return fmt.Errorf("demoting to secondary: %w", err)
		}
		h.log.Info("successfully demoted to secondary")
	}

	return nil
}

func (h *resourceReconcileRequestHandler) setConditionIfNeeded(
	conditionType string,
	status metav1.ConditionStatus,
	reason,
	message string,
	obsGen int64,
) error {
	return api.PatchStatusWithConflictRetry(
		h.ctx, h.cl, h.rvr,
		func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
			rvr.InitializeStatusConditions()
			meta.SetStatusCondition(
				&rvr.Status.Conditions,
				metav1.Condition{
					Type:               conditionType,
					Status:             status,
					Reason:             reason,
					Message:            message,
					ObservedGeneration: obsGen,
				},
			)
			rvr.RecalculateStatusConditionReady()
			return nil
		},
	)
}

func (h *resourceReconcileRequestHandler) failAdjustmentWithReason(
	logMsg string,
	err error,
	reason string,
) error {
	h.log.Error("failed to write resource config", "error", err)
	return errors.Join(
		err,
		h.setConditionIfNeeded(
			v1alpha2.ConditionTypeConfigurationAdjusted,
			metav1.ConditionFalse,
			reason,
			logMsg+": "+err.Error(),
			h.rvr.Generation,
		),
	)
}
