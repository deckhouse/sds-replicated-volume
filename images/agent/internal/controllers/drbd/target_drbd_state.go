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

package drbd

import (
	"fmt"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/maps"
)

// computeTargetDRBDActions computes the DRBD actions needed to converge
// from actual state to intended state. It returns only DRBD-specific actions,
// not K8S object modifications (finalizers, status).
func computeTargetDRBDActions(iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	if iState.IsZero() {
		return
	}

	// DRBD actions require valid actual state.
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
func computeBringUpActions(iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	resourceName := iState.ResourceName()

	// Shared minor pointer for passing between actions
	var allocatedMinor uint

	if !aState.ResourceExists() {
		// Resource doesn't exist - create it
		res = append(res, NewResourceAction{
			ResourceName: resourceName,
			NodeID:       iState.NodeID(),
		})

		// Set resource options
		res = append(res, computeResourceOptionsAction(resourceName, iState)...)

		// Create volume/minor
		res = append(res, NewMinorAction{
			ResourceName:   resourceName,
			Volume:         0,
			AllocatedMinor: &allocatedMinor,
		})

		// Attach backing storage for diskful resources
		if iState.Type() == v1alpha1.DRBDResourceTypeDiskful && iState.BackingDisk() != "" {
			// Create metadata first
			res = append(res, CreateMetadataAction{
				Minor:      &allocatedMinor,
				BackingDev: iState.BackingDisk(),
			})
			res = append(res, AttachAction{
				Minor:    &allocatedMinor,
				LowerDev: iState.BackingDisk(),
				MetaDev:  "internal",
				MetaIdx:  "internal",
			})
			// Set disk options
			res = append(res, computeDiskOptionsAction(&allocatedMinor)...)
		}
	} else {
		// Resource exists - reconcile options

		// Check if disk needs to be changed (detach before attach)
		if len(aState.Volumes()) > 0 {
			actualVol := aState.Volumes()[0]
			actualDisk := actualVol.BackingDisk()
			intendedDisk := iState.BackingDisk()

			// Detach if:
			// 1. Currently has a disk attached (actualDisk != "")
			// 2. AND intended disk is different (including going diskless)
			if actualDisk != "" && actualDisk != intendedDisk {
				minor := uint(actualVol.Minor())
				res = append(res, DetachAction{Minor: &minor})
			}
		}

		res = append(res, computeResourceOptionsActionReconcile(resourceName, iState, aState)...)
		res = append(res, computeDiskOptionsActionReconcile(aState)...)
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
			CRAMHMACAlg:  string(iPeer.SharedSecretAlg()),
			RRConflict:   "retry-connect",
		})
		// Set net options
		res = append(res, computeNetOptionsAction(resourceName, iPeer)...)
		res = append(res, computePathActions(resourceName, iPeer, nil)...)
		res = append(res, ConnectAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
		})
	}

	for nodeID, pair := range existing {
		// Reconcile net options
		res = append(res, computeNetOptionsActionReconcile(resourceName, pair.Left, pair.Right)...)
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

func computeResourceOptionsAction(resourceName string, iState IntendedDRBDState) (res DRBDActions) {
	autoPromote := false
	quorum := uint(iState.Quorum())
	quorumMinRedundancy := uint(iState.QuorumMinimumRedundancy())

	res = append(res, ResourceOptionsAction{
		ResourceName:               resourceName,
		AutoPromote:                &autoPromote,
		OnNoQuorum:                 "suspend-io",
		OnNoDataAccessible:         "suspend-io",
		OnSuspendedPrimaryOutdated: "force-secondary",
		Quorum:                     &quorum,
		QuorumMinimumRedundancy:    &quorumMinRedundancy,
	})
	return res
}

func computeResourceOptionsActionReconcile(resourceName string, iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	var changed bool
	action := ResourceOptionsAction{ResourceName: resourceName}

	// Check quorum
	intendedQuorum := uint(iState.Quorum())
	actualQuorum := aState.Quorum()
	if !quorumMatches(intendedQuorum, actualQuorum) {
		action.Quorum = &intendedQuorum
		changed = true
	}

	// Check quorum minimum redundancy
	intendedQMR := uint(iState.QuorumMinimumRedundancy())
	actualQMR := aState.QuorumMinimumRedundancy()
	if !quorumMatches(intendedQMR, actualQMR) {
		action.QuorumMinimumRedundancy = &intendedQMR
		changed = true
	}

	// Check auto-promote
	if aState.AutoPromote() {
		autoPromote := false
		action.AutoPromote = &autoPromote
		changed = true
	}

	// Check on-no-quorum
	if aState.OnNoQuorum() != "suspend-io" {
		action.OnNoQuorum = "suspend-io"
		changed = true
	}

	// Check on-no-data-accessible
	if aState.OnNoDataAccessible() != "suspend-io" {
		action.OnNoDataAccessible = "suspend-io"
		changed = true
	}

	// Check on-suspended-primary-outdated
	if aState.OnSuspendedPrimaryOutdated() != "force-secondary" {
		action.OnSuspendedPrimaryOutdated = "force-secondary"
		changed = true
	}

	if changed {
		res = append(res, action)
	}
	return res
}

func computeDiskOptionsAction(minor *uint) (res DRBDActions) {
	discardZeroes := false
	rsDiscardGran := uint(8192)
	res = append(res, DiskOptionsAction{
		Minor:                  minor,
		DiscardZeroesIfAligned: &discardZeroes,
		RsDiscardGranularity:   &rsDiscardGran,
	})
	return res
}

func computeDiskOptionsActionReconcile(aState ActualDRBDState) (res DRBDActions) {
	for _, vol := range aState.Volumes() {
		// Skip volumes without backing disk (diskless or no show data)
		if vol.BackingDisk() == "" {
			continue
		}

		var changed bool
		minor := uint(vol.Minor())
		action := DiskOptionsAction{Minor: &minor}

		// Check discard-zeroes-if-aligned
		if vol.DiscardZeroesIfAligned() {
			discardZeroes := false
			action.DiscardZeroesIfAligned = &discardZeroes
			changed = true
		}

		// Check rs-discard-granularity
		if vol.RsDiscardGranularity() != "8192" {
			rsDiscardGran := uint(8192)
			action.RsDiscardGranularity = &rsDiscardGran
			changed = true
		}

		if changed {
			res = append(res, action)
		}
	}
	return res
}

func computeNetOptionsAction(resourceName string, iPeer IntendedPeer) (res DRBDActions) {
	allowTwoPrimaries := false // default, will be set from spec if needed
	allowRemoteRead := iPeer.AllowRemoteRead()

	res = append(res, NetOptionsAction{
		ResourceName:      resourceName,
		PeerNodeID:        iPeer.NodeID(),
		AllowTwoPrimaries: &allowTwoPrimaries,
		AllowRemoteRead:   &allowRemoteRead,
	})
	return res
}

func computeNetOptionsActionReconcile(resourceName string, iPeer IntendedPeer, aPeer ActualPeer) (res DRBDActions) {
	var changed bool
	action := NetOptionsAction{
		ResourceName: resourceName,
		PeerNodeID:   iPeer.NodeID(),
	}

	// Check allow-remote-read
	if iPeer.AllowRemoteRead() != aPeer.AllowRemoteRead() {
		allowRemoteRead := iPeer.AllowRemoteRead()
		action.AllowRemoteRead = &allowRemoteRead
		changed = true
	}

	// Note: AllowTwoPrimaries comes from IntendedDRBDState, not IntendedPeer
	// We'll skip it here since it requires access to iState

	if changed {
		res = append(res, action)
	}
	return res
}

func quorumMatches(intended uint, actual string) bool {
	if intended == 0 {
		return actual == "off"
	}
	return actual == fmt.Sprintf("%d", intended)
}

func computePathActions(resourceName string, iPeer IntendedPeer, aPeer ActualPeer) (res DRBDActions) {
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
