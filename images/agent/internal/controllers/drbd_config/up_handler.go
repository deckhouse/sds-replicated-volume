package drbdconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"

	u "github.com/deckhouse/sds-common-lib/utils"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type UpHandler struct {
	cl       client.Client
	log      *slog.Logger
	rvr      *v1alpha3.ReplicatedVolumeReplica
	rv       *v1alpha3.ReplicatedVolume
	lvg      *snc.LVMVolumeGroup   // will be nil for rvr.spec.type != "Diskful"
	llv      *snc.LVMLogicalVolume // will be nil for rvr.spec.type != "Diskful"
	nodeName string
}

func (h *UpHandler) Handle(ctx context.Context) error {
	if err := h.ensureRVRFinalizers(ctx); err != nil {
		return err
	}
	if h.llv != nil {
		if err := h.ensureLLVFinalizers(ctx); err != nil {
			return err
		}
	}

	statusPatch := client.MergeFrom(h.rvr.DeepCopy())

	err := h.handleDRBDOperation(ctx)

	var drbdErr drbdAPIError

	if errors.As(err, &drbdErr) {
		if h.rvr.Status.DRBD.Errors == nil {
			h.rvr.Status.DRBD.Errors = &v1alpha3.DRBDErrors{}
		}

		drbdErr.ToDRBDErrors(h.rvr.Status.DRBD.Errors)
	}

	if patchErr := h.cl.Status().Patch(ctx, h.rvr, statusPatch); patchErr != nil {
		return fmt.Errorf("patching status: %w", errors.Join(patchErr, err))
	}

	return err
}

func (h *UpHandler) ensureRVRFinalizers(ctx context.Context) error {
	patch := client.MergeFrom(h.rvr.DeepCopy())
	if !slices.Contains(h.rvr.Finalizers, v1alpha3.AgentAppFinalizer) {
		h.rvr.Finalizers = append(h.rvr.Finalizers, v1alpha3.AgentAppFinalizer)
	}
	if !slices.Contains(h.rvr.Finalizers, v1alpha3.ControllerAppFinalizer) {
		h.rvr.Finalizers = append(h.rvr.Finalizers, v1alpha3.ControllerAppFinalizer)
	}
	if err := h.cl.Patch(ctx, h.rvr, patch); err != nil {
		return fmt.Errorf("patching rvr finalizers: %w", err)
	}
	return nil
}

func (h *UpHandler) ensureLLVFinalizers(ctx context.Context) error {
	patch := client.MergeFrom(h.llv.DeepCopy())
	if !slices.Contains(h.llv.Finalizers, v1alpha3.AgentAppFinalizer) {
		h.llv.Finalizers = append(h.llv.Finalizers, v1alpha3.AgentAppFinalizer)
	}
	if err := h.cl.Patch(ctx, h.llv, patch); err != nil {
		return fmt.Errorf("patching llv finalizers: %w", err)
	}
	return nil
}

func (h *UpHandler) handleDRBDOperation(ctx context.Context) error {
	rvName := h.rvr.Spec.ReplicatedVolumeName

	// prepare patch for status errors/actual fields

	if h.rvr.Status == nil {
		h.rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}
	if h.rvr.Status.DRBD == nil {
		h.rvr.Status.DRBD = &v1alpha3.DRBD{}
	}

	// write config to temp file
	regularFilePath, tmpFilePath := FilePaths(rvName)
	if err := h.writeResourceConfig(tmpFilePath); err != nil {
		return fmt.Errorf("writing to %s: %w", tmpFilePath, fileSystemOperationError{err})
	}

	// test temp file
	if err := drbdadm.ExecuteShNop(ctx, tmpFilePath, regularFilePath); err != nil {
		return configurationCommandError{err}
	}

	// move using afero wrapper to allow test FS swap
	if err := FS.Rename(tmpFilePath, regularFilePath); err != nil {
		return fmt.Errorf("renaming %s -> %s: %w", tmpFilePath, regularFilePath, fileSystemOperationError{err})
	}

	//
	if h.rvr.Spec.Type == "Diskful" {
		exists, err := drbdadm.ExecuteDumpMDMetadataExists(ctx, rvName)
		if err != nil {
			return fmt.Errorf("dumping metadata: %w", configurationCommandError{err})
		}

		if !exists {
			if err := drbdadm.ExecuteCreateMD(ctx, rvName); err != nil {
				return fmt.Errorf("creating metadata: %w", configurationCommandError{err})
			}
		}

		// initial sync?
		noPeers := h.rvr.Status.DRBD.Config.PeersInitialized &&
			len(h.rvr.Status.DRBD.Config.Peers) == 0

		upToDate := h.rvr.Status != nil &&
			h.rvr.Status.DRBD != nil &&
			h.rvr.Status.DRBD.Status != nil &&
			len(h.rvr.Status.DRBD.Status.Devices) > 0 &&
			h.rvr.Status.DRBD.Status.Devices[0].DiskState != "UpToDate"

		alreadyCompleted := h.rvr.Status != nil &&
			h.rvr.Status.DRBD != nil &&
			h.rvr.Status.DRBD.Actual.InitialSyncCompleted

		if noPeers && !upToDate && !alreadyCompleted {
			if err := drbdadm.ExecutePrimaryForce(ctx, rvName); err != nil {
				return fmt.Errorf("promoting resource '%s' for initial sync: %w", rvName, configurationCommandError{err})
			}

			if err := drbdadm.ExecuteSecondary(ctx, rvName); err != nil {
				return fmt.Errorf("demoting resource '%s' after initil sync: %w", rvName, configurationCommandError{err})
			}
		}
	}

	// Set actual fields
	if h.rvr.Status.DRBD.Actual == nil {
		h.rvr.Status.DRBD.Actual = &v1alpha3.DRBDActual{}
	}
	h.rvr.Status.DRBD.Actual.InitialSyncCompleted = true
	if h.llv != nil {
		h.rvr.Status.DRBD.Actual.Disk = v1alpha3.SprintDRBDDisk(
			h.lvg.Spec.ActualVGNameOnTheNode,
			h.llv.Spec.ActualLVNameOnTheNode,
		)
	}

	// up & adjust
	isUp, err := drbdadm.ExecuteStatusIsUp(ctx, rvName)
	if err != nil {
		return fmt.Errorf("checking if resource '%s' is up: %w", rvName, configurationCommandError{err})
	}

	if !isUp {
		if err := drbdadm.ExecuteUp(ctx, rvName); err != nil {
			return fmt.Errorf("upping the resource '%s': %w", rvName, configurationCommandError{err})
		}
	}

	if err := drbdadm.ExecuteAdjust(ctx, rvName); err != nil {
		return fmt.Errorf("adjusting the resource '%s': %w", rvName, configurationCommandError{err})
	}

	return nil
}

func (h *UpHandler) writeResourceConfig(filepath string) error {
	rootSection := &drbdconf.Section{}

	err := drbdconf.Marshal(
		&v9.Config{Resources: []*v9.Resource{h.generateResourceConfig()}},
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

	file, err := FS.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
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

func (h *UpHandler) generateResourceConfig() *v9.Resource {
	res := &v9.Resource{
		Name: h.rvr.Spec.ReplicatedVolumeName,
		Net: &v9.Net{
			Protocol:          v9.ProtocolC,
			SharedSecret:      h.rv.Status.DRBD.Config.SharedSecret,
			CRAMHMACAlg:       h.rv.Status.DRBD.Config.SharedSecretAlg,
			RRConflict:        v9.RRConflictPolicyRetryConnect,
			AllowTwoPrimaries: h.rv.Status.DRBD.Config.AllowTwoPrimaries,
		},
		Options: &v9.Options{
			OnNoQuorum:                 v9.OnNoQuorumPolicySuspendIO,
			OnNoDataAccessible:         v9.OnNoDataAccessiblePolicySuspendIO,
			OnSuspendedPrimaryOutdated: v9.OnSuspendedPrimaryOutdatedPolicyForceSecondary,
			AutoPromote:                u.Ptr(false),
		},
	}

	// quorum
	if h.rv.Status.DRBD.Config.Quorum == 0 {
		res.Options.Quorum = &v9.QuorumOff{}
	} else {
		res.Options.Quorum = &v9.QuorumNumeric{
			Value: int(h.rv.Status.DRBD.Config.Quorum),
		}
	}
	if h.rv.Status.DRBD.Config.QuorumMinimumRedundancy == 0 {
		res.Options.QuorumMinimumRedundancy = &v9.QuorumMinimumRedundancyOff{}
	} else {
		res.Options.QuorumMinimumRedundancy = &v9.QuorumMinimumRedundancyNumeric{
			Value: int(h.rv.Status.DRBD.Config.QuorumMinimumRedundancy),
		}
	}

	// current node
	h.populateResourceForNode(res, h.nodeName, *h.rvr.Status.DRBD.Config.NodeId, nil)

	// peers
	for peerName, peer := range h.rvr.Status.DRBD.Config.Peers {
		if peerName == h.nodeName {
			h.log.Warn("Current node appeared in a peer list. Ignored.")
			continue
		}
		h.populateResourceForNode(res, peerName, peer.NodeId, &peer)
	}

	return res
}

func (h *UpHandler) populateResourceForNode(
	res *v9.Resource,
	nodeName string,
	nodeID uint,
	peerOptions *v1alpha3.Peer, // nil for current node
) {
	isCurrentNode := peerOptions == nil

	onSection := &v9.On{
		HostNames: []string{nodeName},
		NodeID:    u.Ptr(nodeID),
	}

	// volumes

	vol := &v9.Volume{
		Number:   u.Ptr(0),
		Device:   u.Ptr(v9.DeviceMinorNumber(*h.rv.Status.DRBD.Config.DeviceMinor)),
		MetaDisk: &v9.VolumeMetaDiskInternal{},
	}

	// some information is node-specific, so skip for other nodes
	if isCurrentNode {
		if h.llv == nil {
			vol.Disk = &v9.VolumeDiskNone{}
		} else {
			vol.Disk = u.Ptr(v9.VolumeDisk(v1alpha3.SprintDRBDDisk(
				h.lvg.Spec.ActualVGNameOnTheNode,
				h.llv.Spec.ActualLVNameOnTheNode,
			)))
		}
		vol.DiskOptions = &v9.DiskOptions{
			DiscardZeroesIfAligned: u.Ptr(false),
			RsDiscardGranularity:   u.Ptr(uint(8192)),
		}
	} else {
		if peerOptions.Diskless {
			vol.Disk = &v9.VolumeDiskNone{}
		} else {
			vol.Disk = u.Ptr(v9.VolumeDisk("/not/used"))
		}
	}
	onSection.Volumes = append(onSection.Volumes, vol)

	res.On = append(res.On, onSection)

	// connections
	if !isCurrentNode {
		con := &v9.Connection{
			Hosts: []v9.HostAddress{
				apiAddressToV9HostAddress(h.nodeName, *h.rvr.Status.DRBD.Config.Address),
				apiAddressToV9HostAddress(nodeName, peerOptions.Address),
			},
		}

		res.Connections = append(res.Connections, con)
	}
}

func apiAddressToV9HostAddress(hostname string, address v1alpha3.Address) v9.HostAddress {
	return v9.HostAddress{
		Name:            hostname,
		AddressWithPort: fmt.Sprintf("%s:%d", address.IPv4, address.Port),
		AddressFamily:   "ipv4",
	}
}
