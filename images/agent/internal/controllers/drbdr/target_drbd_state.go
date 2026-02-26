/*
Copyright 2026 Flant JSC

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

package drbdr

import (
	"fmt"
	"slices"
	"strings"

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
// Uses convergent pattern: each section ensures its component exists and is configured correctly,
// regardless of whether the resource was just created or already existed.
func computeBringUpActions(iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	resourceName := iState.ResourceName()

	// Shared minor pointer for passing between actions when creating new minor
	var allocatedMinor uint

	// 1. Ensure resource exists
	if !aState.ResourceExists() {
		res = append(res, NewResourceAction{
			ResourceName: resourceName,
			NodeID:       iState.NodeID(),
		})
	}

	// 2. Reconcile resource options (handles both new and existing)
	res = append(res, computeResourceOptions(resourceName, iState, aState)...)

	// 3. Compute minor actions (create if missing)
	minor, minorActions := computeMinorActions(resourceName, &allocatedMinor, iState, aState)
	res = append(res, minorActions...)

	// 4. Handle disk (detach if changing, attach if missing, reconcile options)
	res = append(res, computeDiskActions(minor, iState, aState)...)

	// 5. Peers
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
			PeerName:     fmt.Sprintf("peer-%d", nodeID),
			Protocol:     string(iPeer.Protocol()),
			SharedSecret: iPeer.SharedSecret(),
			CRAMHMACAlg:  string(iPeer.SharedSecretAlg()),
			RRConflict:   iPeer.RRConflict(),
		})
		// Set net options
		res = append(res, computeNetOptionsAction(resourceName, iPeer, iState.AllowTwoPrimaries())...)
		res = append(res, computePathActions(resourceName, iPeer, nil)...)
		res = append(res, ConnectAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
		})
	}

	for nodeID, pair := range existing {
		iPeer, aPeer := pair.Left, pair.Right

		// Reconcile net options (protocol, shared-secret, cram-hmac-alg, allow-two-primaries, allow-remote-read)
		res = append(res, computeNetOptionsActionReconcile(resourceName, iPeer, aPeer, iState.AllowTwoPrimaries())...)
		res = append(res, computePathActions(resourceName, iPeer, aPeer)...)
		// Connect is only valid when peer is in StandAlone state.
		// Any other state means connection is either active, in progress, or in error recovery,
		// and calling connect will fail with "Device has a net-config".
		if aPeer.ConnectionState() == v1alpha1.ConnectionStateStandAlone.String() {
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
		res = append(res, ForgetPeerAction{
			ResourceName: resourceName,
			PeerNodeID:   nodeID,
		})
	}

	// Resize action (only for diskful resources with existing volumes)
	res = append(res, computeResizeAction(iState, aState)...)

	// Role change actions (should happen last, after everything is configured)
	res = append(res, computeRoleAction(resourceName, iState, aState)...)

	return res
}

// computeMinorActions returns a pointer to the minor number and any actions needed to create it.
// If a volume already exists, returns its minor; otherwise returns the allocatedMinor pointer
// that will be filled by NewMinorAction during execution.
func computeMinorActions(resourceName string, allocatedMinor *uint, iState IntendedDRBDState, aState ActualDRBDState) (*uint, DRBDActions) {
	if len(aState.Volumes()) > 0 {
		m := uint(aState.Volumes()[0].Minor())
		return &m, nil
	}
	return allocatedMinor, DRBDActions{NewMinorAction{
		ResourceName:   resourceName,
		Volume:         0,
		Diskless:       iState.Type() == v1alpha1.DRBDResourceTypeDiskless,
		AllocatedMinor: allocatedMinor,
	}}
}

// computeDiskActions handles all disk-related actions: detach, attach, and options.
// It ensures the disk state converges to the intended state.
func computeDiskActions(minor *uint, iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	actualDisk := ""
	if len(aState.Volumes()) > 0 {
		actualDisk = aState.Volumes()[0].BackingDisk()
	}
	intendedDisk := iState.BackingDisk()

	// Detach if disk is different (including going diskless)
	if actualDisk != "" && actualDisk != intendedDisk {
		res = append(res, DetachAction{Minor: minor})
		return // Don't attach in same cycle as detach
	}

	// Attach if needed (diskful with backing disk, but no current disk)
	if actualDisk == "" && intendedDisk != "" && iState.Type() == v1alpha1.DRBDResourceTypeDiskful {
		res = append(res, CreateMetadataAction{
			Minor:      minor,
			BackingDev: intendedDisk,
		})
		res = append(res, AttachAction{
			Minor:    minor,
			LowerDev: intendedDisk,
			MetaDev:  intendedDisk, // same device for internal metadata
			MetaIdx:  "internal",
		})
		res = append(res, computeDiskOptionsAction(minor, iState)...)
		return
	}

	// Reconcile disk options for existing attached disk
	if actualDisk != "" {
		res = append(res, computeDiskOptionsActionReconcile(iState, aState)...)
	}

	return
}

// computeResourceOptions dispatches to full-set or reconcile variant based on resource existence.
func computeResourceOptions(resourceName string, iState IntendedDRBDState, aState ActualDRBDState) DRBDActions {
	if !aState.ResourceExists() {
		return computeResourceOptionsAction(resourceName, iState)
	}
	return computeResourceOptionsActionReconcile(resourceName, iState, aState)
}

func computeResourceOptionsAction(resourceName string, iState IntendedDRBDState) (res DRBDActions) {
	autoPromote := iState.AutoPromote()
	quorum := uint(iState.Quorum())
	quorumMinRedundancy := uint(iState.QuorumMinimumRedundancy())

	res = append(res, ResourceOptionsAction{
		ResourceName:               resourceName,
		AutoPromote:                &autoPromote,
		OnNoQuorum:                 iState.OnNoQuorum(),
		OnNoDataAccessible:         iState.OnNoDataAccessible(),
		OnSuspendedPrimaryOutdated: iState.OnSuspendedPrimaryOutdated(),
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
	if iState.AutoPromote() != aState.AutoPromote() {
		autoPromote := iState.AutoPromote()
		action.AutoPromote = &autoPromote
		changed = true
	}

	// Check on-no-quorum
	if iState.OnNoQuorum() != aState.OnNoQuorum() {
		action.OnNoQuorum = iState.OnNoQuorum()
		changed = true
	}

	// Check on-no-data-accessible
	if iState.OnNoDataAccessible() != aState.OnNoDataAccessible() {
		action.OnNoDataAccessible = iState.OnNoDataAccessible()
		changed = true
	}

	// Check on-suspended-primary-outdated
	if iState.OnSuspendedPrimaryOutdated() != aState.OnSuspendedPrimaryOutdated() {
		action.OnSuspendedPrimaryOutdated = iState.OnSuspendedPrimaryOutdated()
		changed = true
	}

	if changed {
		res = append(res, action)
	}
	return res
}

func computeDiskOptionsAction(minor *uint, iState IntendedDRBDState) (res DRBDActions) {
	discardZeroes := iState.DiscardZeroesIfAligned()
	rsDiscardGran := iState.RsDiscardGranularity()
	res = append(res, DiskOptionsAction{
		Minor:                  minor,
		DiscardZeroesIfAligned: &discardZeroes,
		RsDiscardGranularity:   &rsDiscardGran,
	})
	return res
}

func computeDiskOptionsActionReconcile(iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	for _, vol := range aState.Volumes() {
		// Skip volumes without backing disk (diskless or no show data)
		if vol.BackingDisk() == "" {
			continue
		}

		var changed bool
		minor := uint(vol.Minor())
		action := DiskOptionsAction{Minor: &minor}

		// Check discard-zeroes-if-aligned
		if iState.DiscardZeroesIfAligned() != vol.DiscardZeroesIfAligned() {
			discardZeroes := iState.DiscardZeroesIfAligned()
			action.DiscardZeroesIfAligned = &discardZeroes
			changed = true
		}

		// Check rs-discard-granularity
		if fmt.Sprintf("%d", iState.RsDiscardGranularity()) != vol.RsDiscardGranularity() {
			rsDiscardGran := iState.RsDiscardGranularity()
			action.RsDiscardGranularity = &rsDiscardGran
			changed = true
		}

		if changed {
			res = append(res, action)
		}
	}
	return res
}

func computeNetOptionsAction(resourceName string, iPeer IntendedPeer, allowTwoPrimaries bool) (res DRBDActions) {
	allowRemoteRead := iPeer.AllowRemoteRead()

	res = append(res, NetOptionsAction{
		ResourceName:      resourceName,
		PeerNodeID:        iPeer.NodeID(),
		AllowTwoPrimaries: &allowTwoPrimaries,
		AllowRemoteRead:   &allowRemoteRead,
	})
	return res
}

func computeNetOptionsActionReconcile(resourceName string, iPeer IntendedPeer, aPeer ActualPeer, allowTwoPrimaries bool) (res DRBDActions) {
	var changed bool
	action := NetOptionsAction{
		ResourceName: resourceName,
		PeerNodeID:   iPeer.NodeID(),
	}

	// Check protocol
	if string(iPeer.Protocol()) != aPeer.Protocol() {
		protocol := string(iPeer.Protocol())
		action.Protocol = &protocol
		changed = true
	}

	// Check shared-secret
	if iPeer.SharedSecret() != aPeer.SharedSecret() {
		sharedSecret := iPeer.SharedSecret()
		action.SharedSecret = &sharedSecret
		changed = true
	}

	// Check cram-hmac-alg (case-insensitive because DRBD normalizes to lowercase)
	if !strings.EqualFold(string(iPeer.SharedSecretAlg()), aPeer.SharedSecretAlg()) {
		cramHMACAlg := strings.ToLower(string(iPeer.SharedSecretAlg()))
		action.CRAMHMACAlg = &cramHMACAlg
		changed = true
	}

	// Check allow-two-primaries
	if allowTwoPrimaries != aPeer.AllowTwoPrimaries() {
		action.AllowTwoPrimaries = &allowTwoPrimaries
		changed = true
	}

	// Check allow-remote-read
	if iPeer.AllowRemoteRead() != aPeer.AllowRemoteRead() {
		allowRemoteRead := iPeer.AllowRemoteRead()
		action.AllowRemoteRead = &allowRemoteRead
		changed = true
	}

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

// pathKey creates a unique key for a path.
func pathKey(localAddr, remoteAddr string) string {
	return localAddr + "->" + remoteAddr
}

// formatAddr formats IP and port as "ip:port".
func formatAddr(ip string, port uint) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

// computeResizeAction computes resize action if the intended usable size is
// larger than actual. The intended size is the lower volume (backing device)
// size; it is converted to usable size by subtracting DRBD metadata overhead
// before comparison.
//
// The resize command re-examines the backing device and uses whatever space is
// available. If the backing device hasn't actually grown, drbdsetup returns
// ErrResizeBackingNotGrown which ResizeAction treats as a no-op.
func computeResizeAction(iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	if iState.Type() != v1alpha1.DRBDResourceTypeDiskful || iState.Size() == 0 {
		return
	}

	if len(aState.Volumes()) == 0 {
		return
	}

	actualVol := aState.Volumes()[0]

	if actualVol.BackingDisk() == "" || actualVol.DiskState() == "Diskless" {
		return
	}

	intendedUsable := drbdUsableSizeBytes(iState.Size())
	if intendedUsable > actualVol.Size() {
		minor := uint(actualVol.Minor())
		res = append(res, ResizeAction{Minor: &minor})
	}

	return
}

// DRBD internal metadata size calculation.
// Must stay in sync with images/controller/internal/drbd_size/drbd_size.go.
const (
	drbdMaxPeers          = 7
	drbdSectorSize        = 512
	drbdBmSectPerBit      = 8  // sectors per bitmap bit (1 << (12 - 9))
	drbdSuperblockSectors = 8  // 4096 bytes / 512
	drbdALSizeSectors     = 64 // 1 stripe * 32KB / 512
)

func drbdAlign(x, a uint64) uint64 {
	return (x + a - 1) &^ (a - 1)
}

func drbdMetadataSectors(lowerSectors uint64) uint64 {
	bits := drbdAlign(drbdAlign(lowerSectors, drbdBmSectPerBit)/drbdBmSectPerBit, 64)
	bitmapSectors := drbdAlign(bits/8*drbdMaxPeers, 4096) / drbdSectorSize
	return bitmapSectors + drbdSuperblockSectors + drbdALSizeSectors
}

// drbdUsableSizeBytes converts a lower volume (backing device) size in bytes
// to the DRBD usable capacity in bytes, subtracting internal metadata overhead.
func drbdUsableSizeBytes(lowerBytes int64) int64 {
	lowerSectors := uint64(lowerBytes) / drbdSectorSize
	usableSectors := lowerSectors - drbdMetadataSectors(lowerSectors)
	return int64(usableSectors * drbdSectorSize)
}

// computeRoleAction computes role change action if actual role differs from intended.
func computeRoleAction(resourceName string, iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	intendedRole := iState.Role()
	actualRole := aState.Role()

	// If intended role is Primary and actual is not Primary
	if intendedRole == v1alpha1.DRBDRolePrimary && actualRole != "Primary" {
		res = append(res, PrimaryAction{
			ResourceName: resourceName,
			Force:        false,
		})
	}

	// If intended role is Secondary and actual is not Secondary
	if intendedRole == v1alpha1.DRBDRoleSecondary && actualRole != "Secondary" {
		res = append(res, SecondaryAction{
			ResourceName: resourceName,
			Force:        false,
		})
	}

	return
}
