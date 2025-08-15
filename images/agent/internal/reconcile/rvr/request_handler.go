package rvr

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	. "github.com/deckhouse/sds-common-lib/u"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type resourceReconcileRequestHandler struct {
	ctx      context.Context
	log      *slog.Logger
	cl       client.Client
	nodeName string
	cfg      *ReconcilerClusterConfig
	rvr      *v1alpha2.ReplicatedVolumeReplica
}

func (h *resourceReconcileRequestHandler) Handle() error {
	if err := h.writeResourceConfig(); err != nil {
		h.log.Error("failed to write resource config", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
		h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionFalse, v1alpha2.ReasonConfigurationFailed, err.Error())
		return err
	}

	exists, err := drbdadm.ExecuteDumpMD_MetadataExists(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		h.log.Error("failed to check metadata existence", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
		h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionFalse, v1alpha2.ReasonMetadataCheckFailed, err.Error())
		return fmt.Errorf("ExecuteDumpMD_MetadataExists: %w", err)
	}

	if !exists {
		if err := drbdadm.ExecuteCreateMD(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			h.log.Error("failed to create metadata", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
			h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionFalse, v1alpha2.ReasonMetadataCreationFailed, err.Error())
			return fmt.Errorf("ExecuteCreateMD: %w", err)
		}

		h.log.Info("successfully created metadata", "resource", h.rvr.Spec.ReplicatedVolumeName)
	}

	isUp, err := drbdadm.ExecuteStatus_IsUp(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		h.log.Error("failed to check resource status", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
		h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionFalse, v1alpha2.ReasonStatusCheckFailed, err.Error())
		return fmt.Errorf("ExecuteStatus_IsUp: %w", err)
	}

	if !isUp {
		if err := drbdadm.ExecuteUp(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			h.log.Error("failed to bring up resource", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
			h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionFalse, v1alpha2.ReasonResourceUpFailed, err.Error())
			return fmt.Errorf("ExecuteUp: %w", err)
		}

		h.log.Info("successfully brought up resource", "resource", h.rvr.Spec.ReplicatedVolumeName)
	}

	if err := drbdadm.ExecuteAdjust(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		h.log.Error("failed to adjust resource", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
		h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionFalse, v1alpha2.ReasonAdjustmentFailed, err.Error())
		return fmt.Errorf("ExecuteAdjust: %w", err)
	}

	h.log.Info("successfully adjusted resource", "resource", h.rvr.Spec.ReplicatedVolumeName)

	if err := h.handlePrimarySecondary(); err != nil {
		return fmt.Errorf("handling primary/secondary: %w", err)
	}

	h.setConditionIfNeeded(v1alpha2.ConditionTypeReady, metav1.ConditionTrue, v1alpha2.ReasonReady, "Replica is configured and operational")

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
			Protocol:     v9.ProtocolC,
			SharedSecret: h.rvr.Spec.SharedSecret,
		},
	}

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
			vol.Disk = Ptr(v9.VolumeDisk(volume.Disk))
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
	statusResult, err := drbdsetup.ExecuteStatus(h.ctx)
	if err != nil {
		h.log.Error("failed to get DRBD status", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
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
		h.log.Error("resource not found in DRBD status", "resource", h.rvr.Spec.ReplicatedVolumeName)
		return fmt.Errorf("resource %s not found in DRBD status", h.rvr.Spec.ReplicatedVolumeName)
	}

	desiredRole := "Secondary"
	if h.rvr.Spec.Primary {
		desiredRole = "Primary"
	}

	if currentRole == desiredRole {
		h.log.Debug("DRBD role already correct", "resource", h.rvr.Spec.ReplicatedVolumeName, "role", currentRole)
		conditionStatus := metav1.ConditionFalse
		if h.rvr.Spec.Primary {
			conditionStatus = metav1.ConditionTrue
		}
		h.setConditionIfNeeded(v1alpha2.ConditionTypePrimary, conditionStatus, v1alpha2.ReasonRoleCorrect, fmt.Sprintf("Resource is %s", currentRole))
		return nil
	}

	if h.rvr.Spec.Primary {
		// Check if this is initial synchronization
		isInitialSync := h.isInitialSynchronization()

		var err error
		if isInitialSync {
			h.log.Info("attempting primary promotion with --force during initial synchronization", "resource", h.rvr.Spec.ReplicatedVolumeName)
			err = drbdadm.ExecutePrimaryForce(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
		} else {
			err = drbdadm.ExecutePrimary(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
		}

		if err != nil {
			forceMsg := ""
			if isInitialSync {
				forceMsg = " (with --force)"
			}
			h.log.Error("failed to promote to primary"+forceMsg, "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
			h.setConditionIfNeeded(v1alpha2.ConditionTypePrimary, metav1.ConditionFalse, v1alpha2.ReasonPromotionFailed, err.Error())
			return fmt.Errorf("promoting to primary: %w", err)
		}

		h.log.Info("successfully promoted to primary", "resource", h.rvr.Spec.ReplicatedVolumeName)
		h.setConditionIfNeeded(v1alpha2.ConditionTypePrimary, metav1.ConditionTrue, v1alpha2.ReasonPrimary, "Resource is Primary")

		// Mark initial sync as completed after successful promotion
		if isInitialSync {
			h.setConditionIfNeeded(v1alpha2.ConditionTypeInitialSyncCompleted, metav1.ConditionTrue, v1alpha2.ReasonFirstPrimaryPromoted, "Initial synchronization completed after first successful primary promotion")
		}
	} else {
		if err := drbdadm.ExecuteSecondary(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			h.log.Error("failed to demote to secondary", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
			h.setConditionIfNeeded(v1alpha2.ConditionTypePrimary, metav1.ConditionFalse, v1alpha2.ReasonDemotionFailed, err.Error())
			return fmt.Errorf("demoting to secondary: %w", err)
		}
		h.log.Info("successfully demoted to secondary", "resource", h.rvr.Spec.ReplicatedVolumeName)
		h.setConditionIfNeeded(v1alpha2.ConditionTypePrimary, metav1.ConditionFalse, v1alpha2.ReasonSecondary, "Resource is Secondary")
	}

	return nil
}

func (h *resourceReconcileRequestHandler) setConditionIfNeeded(
	conditionType string,
	status metav1.ConditionStatus,
	reason,
	message string,
) error {
	if h.rvr.Status == nil {
		h.rvr.Status = &v1alpha2.ReplicatedVolumeReplicaStatus{}
		h.rvr.Status.Conditions = []metav1.Condition{}
	}

	for _, condition := range h.rvr.Status.Conditions {
		if condition.Type == conditionType && condition.Status == status && condition.Reason == reason && condition.Message == message {
			return nil
		}
	}

	patch := client.MergeFrom(h.rvr.DeepCopy())

	now := metav1.NewTime(time.Now())
	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	found := false
	for i, condition := range h.rvr.Status.Conditions {
		if condition.Type == conditionType {
			// Preserve transition time when only reason/message changes
			if condition.Status == status {
				newCondition.LastTransitionTime = condition.LastTransitionTime
			}
			h.rvr.Status.Conditions[i] = newCondition
			found = true
			break
		}
	}

	if !found {
		h.rvr.Status.Conditions = append(h.rvr.Status.Conditions, newCondition)
	}

	if err := h.cl.Status().Patch(h.ctx, h.rvr, patch); err != nil {
		return fmt.Errorf("patching RVR status: %w", err)
	}
	h.log.Info("successfully updated condition", "type", conditionType, "resource", h.rvr.Spec.ReplicatedVolumeName)

	return nil
}

// isInitialSynchronization checks if the resource is in initial synchronization state
// by looking for the InitialSyncCompleted condition
func (h *resourceReconcileRequestHandler) isInitialSynchronization() bool {
	if h.rvr.Status == nil || h.rvr.Status.Conditions == nil {
		return true // No status yet, assume initial sync
	}

	for _, condition := range h.rvr.Status.Conditions {
		if condition.Type == v1alpha2.ConditionTypeInitialSyncCompleted && condition.Status == metav1.ConditionTrue {
			return false // Initial sync already completed
		}
	}

	return true // InitialSyncCompleted condition not found or not True
}
