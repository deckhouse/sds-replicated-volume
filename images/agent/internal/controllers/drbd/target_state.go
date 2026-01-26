package drbd

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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

	// DRBD actions

	if !iState.IsUpAndNotInCleanup() {
		// Teardown: generate Down action if resource exists
		if !aState.IsZero() && aState.ResourceExists() {
			res = append(res, DownAction{ResourceName: iState.ResourceName()})
		}
		return res
	}

	// Bring-up sequence
	res = append(res, computeBringUpActions(iState, aState)...)

	return res
}

// computeBringUpActions computes actions to bring DRBD to intended state.
func computeBringUpActions(iState IntendedState, aState ActualState) (res TargetStateActions) {
	resourceName := iState.ResourceName()

	// Check if resource exists
	resourceExists := !aState.IsZero() && aState.ResourceExists()

	if !resourceExists {
		// Resource doesn't exist - create it
		res = append(res, NewResourceAction{
			ResourceName: resourceName,
			NodeID:       iState.NodeID(),
		})

		// Create volume/minor
		res = append(res, NewMinorAction{
			ResourceName:   resourceName,
			MinorAllocator: AllocateNextMinor,
			Volume:         0, // Single volume per resource
		})

		// Attach backing storage for diskful resources
		if iState.Type() == v1alpha1.DRBDResourceTypeDiskful && iState.BackingDisk() != "" {
			res = append(res, AttachAction{
				Minor:    0, // Will be allocated by NewMinorAction
				LowerDev: iState.BackingDisk(),
				MetaDev:  "internal",
				MetaIdx:  "internal",
			})
		}
	}

	// Build maps for peer/path comparison
	actualPeers := make(map[int]ActualPeer)
	if !aState.IsZero() {
		for _, peer := range aState.Peers() {
			actualPeers[peer.NodeID()] = peer
		}
	}

	// Reconcile peers
	for _, iPeer := range iState.Peers() {
		peerNodeID := iPeer.NodeID()
		aPeer, peerExists := actualPeers[int(peerNodeID)]

		if !peerExists {
			// Peer doesn't exist - create it
			res = append(res, NewPeerAction{
				ResourceName: resourceName,
				PeerNodeID:   peerNodeID,
				Protocol:     string(iPeer.Protocol()),
				SharedSecret: iPeer.SharedSecret(),
			})
		}

		// Reconcile paths for this peer
		res = append(res, computePathActions(resourceName, iPeer, aPeer, peerExists)...)

		// Connect to peer if not connected
		if peerExists && !isConnected(aPeer) {
			res = append(res, ConnectAction{
				ResourceName: resourceName,
				PeerNodeID:   peerNodeID,
			})
		} else if !peerExists {
			// New peer needs connection after paths are added
			res = append(res, ConnectAction{
				ResourceName: resourceName,
				PeerNodeID:   peerNodeID,
			})
		}

		// Mark peer as processed
		delete(actualPeers, int(peerNodeID))
	}

	// Remove stale peers (in actual but not in intended)
	for peerNodeID := range actualPeers {
		// First disconnect
		res = append(res, DisconnectAction{
			ResourceName: resourceName,
			PeerNodeID:   uint(peerNodeID),
		})
		// Then remove peer
		res = append(res, DelPeerAction{
			ResourceName: resourceName,
			PeerNodeID:   uint(peerNodeID),
		})
	}

	return res
}

// computePathActions computes actions to reconcile paths for a peer.
func computePathActions(resourceName string, iPeer IntendedPeer, aPeer ActualPeer, peerExists bool) (res TargetStateActions) {
	peerNodeID := iPeer.NodeID()

	// Build map of actual paths
	actualPaths := make(map[string]ActualPath) // key: "local->remote"
	if peerExists && aPeer != nil {
		for _, path := range aPeer.Paths() {
			key := pathKey(path.LocalAddr(), path.RemoteAddr())
			actualPaths[key] = path
		}
	}

	// Add missing paths
	for _, iPath := range iPeer.Paths() {
		localAddr := formatAddr(iPath.LocalIPv4(), iPath.LocalPort())
		remoteAddr := formatAddr(iPath.RemoteIPv4(), iPath.RemotePort())
		key := pathKey(localAddr, remoteAddr)

		if _, exists := actualPaths[key]; !exists {
			res = append(res, NewPathAction{
				ResourceName: resourceName,
				PeerNodeID:   peerNodeID,
				LocalAddr:    localAddr,
				RemoteAddr:   remoteAddr,
			})
		}

		// Mark as processed
		delete(actualPaths, key)
	}

	// Remove stale paths (in actual but not in intended)
	for _, aPath := range actualPaths {
		res = append(res, DelPathAction{
			ResourceName: resourceName,
			PeerNodeID:   peerNodeID,
			LocalAddr:    aPath.LocalAddr(),
			RemoteAddr:   aPath.RemoteAddr(),
		})
	}

	return res
}

// isConnected returns true if the peer is in a connected state.
func isConnected(aPeer ActualPeer) bool {
	if aPeer == nil {
		return false
	}
	state := aPeer.ConnectionState()
	// Connected states from DRBD
	return state == "Connected" || state == "SyncSource" || state == "SyncTarget" ||
		state == "PausedSyncS" || state == "PausedSyncT" || state == "VerifyS" || state == "VerifyT"
}

// pathKey creates a unique key for a path.
func pathKey(localAddr, remoteAddr string) string {
	return localAddr + "->" + remoteAddr
}

// formatAddr formats IP and port as "ip:port".
func formatAddr(ip string, port uint) string {
	return fmt.Sprintf("%s:%d", ip, port)
}
