package rvr

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	. "github.com/deckhouse/sds-replicated-volume/images/agent/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
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
		return err
	}

	exists, err := drbdadm.ExecuteDumpMD_MetadataExists(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return fmt.Errorf("ExecuteDumpMD_MetadataExists: %w", err)
	}

	if !exists {
		if err := drbdadm.ExecuteCreateMD(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			return fmt.Errorf("ExecuteCreateMD: %w", err)
		}

		h.log.Info("successfully created metadata for 'resource'", "resource", h.rvr.Spec.ReplicatedVolumeName)
	}

	isUp, err := drbdadm.ExecuteStatus_IsUp(h.ctx, h.rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		return fmt.Errorf("ExecuteStatus_IsUp: %w", err)
	}

	if !isUp {
		if err := drbdadm.ExecuteUp(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			return fmt.Errorf("ExecuteUp: %w", err)
		}

		h.log.Info("successfully upped 'resource'", "resource", h.rvr.Spec.ReplicatedVolumeName)
	}

	if err := drbdadm.ExecuteAdjust(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		return fmt.Errorf("ExecuteAdjust: %w", err)
	}

	h.log.Info("successfully adjusted 'resource'", "resource", h.rvr.Spec.ReplicatedVolumeName)

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
		AddressWithPort: address.IPv4,
		AddressFamily:   "ipv4",
		Port:            Ptr(address.Port),
	}
}
