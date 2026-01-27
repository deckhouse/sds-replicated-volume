package drbd

import (
	"fmt"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/maps"
)

type TargetStateActions []Action

func computeTargetStateActions(iState IntendedState, aState ActualState) (res TargetStateActions) {
	// actions, which don't need neither [IntendedState], nor [ActualState]

	// (none)

	if iState.IsZero() {
		return
	}

	// actions, which don't need [ActualState]

	if iState.IsUpAndNotInCleanup() {
		res = append(res, AddAgentFinalizerAction{})
	} else {
		// should always go last
		defer func() {
			res = append(res, RemoveAgentFinalizerAction{})
		}()
	}

	res = append(res, ConfigureIPAddressAction{IPv4BySystemNetworkNames: iState.IPv4BySystemNetworkNames()})
	res = append(res, AllocatePortsAction{PortAllocator: DefaultPortCache.Allocate})

	// DRBD actions - require valid actual state
	//
	// If aState.IsZero() is true, it means we failed to get the actual DRBD state
	// (e.g. "drbdsetup show" failed). In this case we skip all DRBD actions since
	// we don't know the current state to compare against.

	if aState.IsZero() {
		return res
	}

	if !iState.IsUpAndNotInCleanup() {
		// Teardown: generate Down action if resource exists
		if aState.ResourceExists() {
			res = append(res, DownAction{ResourceName: iState.ResourceName()})
		}
		return res
	}

	// Bring-up sequence
	res = append(res, computeBringUpActions(iState, aState)...)

	return res
}

// computeBringUpActions computes actions to bring DRBD to intended state.
// Precondition: aState is valid (not zero).
func computeBringUpActions(iState IntendedState, aState ActualState) (res TargetStateActions) {
	resourceName := iState.ResourceName()

	if !aState.ResourceExists() {
		// Resource doesn't exist - create it
		res = append(res, NewResourceAction{
			ResourceName: resourceName,
			NodeID:       iState.NodeID(),
		})

		// Create volume/minor
		var allocatedMinor uint
		res = append(res, NewMinorAction{
			ResourceName:   resourceName,
			Volume:         0,
			AllocatedMinor: &allocatedMinor,
		})

		// Attach backing storage for diskful resources
		if iState.Type() == v1alpha1.DRBDResourceTypeDiskful && iState.BackingDisk() != "" {
			res = append(res, AttachAction{
				Minor:    &allocatedMinor,
				LowerDev: iState.BackingDisk(),
				MetaDev:  "internal",
				MetaIdx:  "internal",
			})
		}
	}

	toAdd, existing, toRemove := maps.IntersectItersKeyFunc(
		slices.Values(iState.Peers()),
		IntendedPeer.NodeID,
		slices.Values(aState.Peers()),
		ActualPeer.NodeID,
	)

	for nodeID, iPeer := range toAdd {
		res = append(res, NewPeerAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
			Protocol:     string(iPeer.Protocol()),
			SharedSecret: iPeer.SharedSecret(),
		})
		res = append(res, computePathActions(resourceName, iPeer, nil)...)
		res = append(res, ConnectAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
		})
	}

	for nodeID, pair := range existing {
		res = append(res, computePathActions(resourceName, pair.Left, pair.Right)...)
		if !isConnected(pair.Right) {
			res = append(res, ConnectAction{
				ResourceName: resourceName,
				PeerNodeID:   nodeID,
			})
		}
	}

	for nodeID := range toRemove {
		res = append(res, DisconnectAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
		})
		res = append(res, DelPeerAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
		})
	}

	return res
}

func computePathActions(resourceName string, iPeer IntendedPeer, aPeer ActualPeer) (res TargetStateActions) {
	peerNodeID := iPeer.NodeID()

	var actualPaths []ActualPath
	if aPeer != nil {
		actualPaths = aPeer.Paths()
	}

	toAdd, _, toRemove := maps.IntersectItersKeyFunc(
		slices.Values(iPeer.Paths()),
		func(p IntendedPath) string {
			return pathKey(formatAddr(p.LocalIPv4(), p.LocalPort()), formatAddr(p.RemoteIPv4(), p.RemotePort()))
		},
		slices.Values(actualPaths),
		func(p ActualPath) string { return pathKey(p.LocalAddr(), p.RemoteAddr()) },
	)

	for _, iPath := range toAdd {
		res = append(res, NewPathAction{
			ResourceName: resourceName,
			PeerNodeID:   peerNodeID,
			LocalAddr:    formatAddr(iPath.LocalIPv4(), iPath.LocalPort()),
			RemoteAddr:   formatAddr(iPath.RemoteIPv4(), iPath.RemotePort()),
		})
	}

	for _, aPath := range toRemove {
		res = append(res, DelPathAction{
			ResourceName: resourceName,
			PeerNodeID:   peerNodeID,
			LocalAddr:    aPath.LocalAddr(),
			RemoteAddr:   aPath.RemoteAddr(),
		})
	}

	return res
}

func isConnected(aPeer ActualPeer) bool {
	if aPeer == nil {
		return false
	}
	return aPeer.ConnectionState() == v1alpha1.ConnectionStateConnected.String()
}

// pathKey creates a unique key for a path.
func pathKey(localAddr, remoteAddr string) string {
	return localAddr + "->" + remoteAddr
}

// formatAddr formats IP and port as "ip:port".
func formatAddr(ip string, port uint) string {
	return fmt.Sprintf("%s:%d", ip, port)
}
