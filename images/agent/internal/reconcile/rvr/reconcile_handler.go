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
	if !h.rvr.IsConfigured() {
		h.log.Debug("rvr not configured, skip")
		return nil
	}

	// validate
	diskless, err := h.rvr.Status.Config.Diskless()
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

	// normalize
	h.rvr.InitializeStatusConditions()

	initialSyncPassed := meta.IsStatusConditionTrue(h.rvr.Status.Conditions, v1alpha2.ConditionTypeInitialSync)
	if err := h.writeResourceConfig(initialSyncPassed); err != nil {
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
				fmt.Sprintf(
					"Initial synchronization should be triggered by adding annotation %s='true' to this resource",
					v1alpha2.AnnotationKeyPrimaryForce,
				),
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
			v1alpha2.ReasonConfigurationAdjustFailed,
		)
	}

	h.log.Info("successfully adjusted resource")

	if !initialSyncPassed {
		h.log.Debug("initial synchronization has not been completed, not doing further configuration")
		return h.setConditionIfNeeded(
			v1alpha2.ConditionTypeConfigurationAdjusted,
			metav1.ConditionFalse,
			v1alpha2.ReasonConfigurationAdjustmentPausedUntilInitialSync,
			"Waiting for initial sync to happen before finishing configuration",
			h.rvr.Generation,
		)
	}

	// Post-InitialSync actions:
	if err := h.handlePrimarySecondary(); err != nil {
		return h.failAdjustmentWithReason(
			"failed to promote/demote",
			err,
			v1alpha2.ReasonPromotionDemotionFailed,
		)
	}

	if err := h.setConditionIfNeeded(
		v1alpha2.ConditionTypeConfigurationAdjusted,
		metav1.ConditionTrue,
		v1alpha2.ReasonConfigurationAdjustmentSucceeded,
		"Replica is configured",
		h.rvr.Generation,
	); err != nil {
		return err
	}
	return nil
}

func (h *resourceReconcileRequestHandler) writeResourceConfig(initialSyncPassed bool) error {
	rootSection := &drbdconf.Section{}

	err := drbdconf.Marshal(
		&v9.Config{Resources: []*v9.Resource{h.generateResourceConfig(initialSyncPassed)}},
		rootSection,
	)
	if err != nil {
		return fmt.Errorf(
			"marshaling resource %s cfg: %w",
			h.rvr.Spec.ReplicatedVolumeName, err,
		)
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

func (h *resourceReconcileRequestHandler) generateResourceConfig(initialSyncPassed bool) *v9.Resource {
	res := &v9.Resource{
		Name: h.rvr.Spec.ReplicatedVolumeName,
		Net: &v9.Net{
			Protocol:          v9.ProtocolC,
			SharedSecret:      h.rvr.Status.Config.SharedSecret,
			RRConflict:        v9.RRConflictPolicyRetryConnect,
			AllowTwoPrimaries: h.rvr.Status.Config.AllowTwoPrimaries,
		},
		Options: &v9.Options{
			OnNoQuorum:                 v9.OnNoQuorumPolicySuspendIO,
			OnNoDataAccessible:         v9.OnNoDataAccessiblePolicySuspendIO,
			OnSuspendedPrimaryOutdated: v9.OnSuspendedPrimaryOutdatedPolicyForceSecondary,
			AutoPromote:                Ptr(false),
		},
	}

	// current node
	h.populateResourceForNode(res, h.nodeName, h.rvr.Status.Config.NodeId, h.rvr.Status.Config.NodeAddress, nil)

	// peers
	for peerName, peer := range h.rvr.Status.Config.Peers {
		if peerName == h.nodeName {
			h.log.Warn("Current node appeared in a peer list. Ignored.")
			continue
		}
		h.populateResourceForNode(res, peerName, peer.NodeId, peer.Address, &peer)
	}

	// Post-InitialSync parameters
	if initialSyncPassed {
		h.updateResourceConfigAfterInitialSync(res)
	}

	return res
}

func (h *resourceReconcileRequestHandler) updateResourceConfigAfterInitialSync(res *v9.Resource) {
	if h.rvr.Status.Config.Quorum == 0 {
		res.Options.Quorum = &v9.QuorumOff{}
	} else {
		res.Options.Quorum = &v9.QuorumNumeric{
			Value: int(h.rvr.Status.Config.Quorum),
		}
	}

	if h.rvr.Status.Config.QuorumMinimumRedundancy == 0 {
		res.Options.QuorumMinimumRedundancy = &v9.QuorumMinimumRedundancyOff{}
	} else {
		res.Options.QuorumMinimumRedundancy = &v9.QuorumMinimumRedundancyNumeric{
			Value: int(h.rvr.Status.Config.QuorumMinimumRedundancy),
		}
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
	for _, volume := range h.rvr.Status.Config.Volumes {
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
			if peerOptions.Diskless {
				vol.Disk = &v9.VolumeDiskNone{}
			} else {
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
				apiAddressToV9HostAddress(h.nodeName, h.rvr.Status.Config.NodeAddress),
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
	if h.rvr.Status.Config.Primary {
		desiredRole = "Primary"
	}

	if currentRole == desiredRole {
		h.log.Debug("DRBD role already correct", "role", currentRole)
		return nil
	}

	if h.rvr.Status.Config.Primary {
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
