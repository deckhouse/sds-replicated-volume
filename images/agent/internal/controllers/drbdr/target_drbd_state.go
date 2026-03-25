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
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
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
		// Teardown: remove symlink before down to prevent dangling references
		res = append(res, RemoveDeviceSymlinkAction{Name: iState.SymlinkName()})
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

	// 3b. Ensure stable device symlink points to the correct minor
	res = append(res, EnsureDeviceSymlinkAction{Name: iState.SymlinkName(), Minor: minor})

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
			VerifyAlg:    iPeer.VerifyAlg(),
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
		res = append(res, computePeerDeviceOptionsAction(resourceName, iPeer, aPeer)...)
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

func generateDeviceUUID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("%016X", binary.BigEndian.Uint64(buf[:]))
}

// computeAttachActions implements the device-uuid decision table and returns the
// sequence of actions to prepare metadata and attach the disk.
func computeAttachActions(aState ActualNonAttachedDiskMetadata, statusUUID string, minor *uint, backingDev string) DRBDActions {
	var actions DRBDActions

	if aState.HasMetadata() {
		diskUUID := aState.DiskDeviceUUID()
		switch {
		case diskUUID != "" && statusUUID == diskUUID:
			// Match — proceed to attach.
		case diskUUID != "" && statusUUID != "" && statusUUID != diskUUID:
			return DRBDActions{FailAction{Err: ConfiguredReasonError(
				fmt.Errorf("backing device %s has device-uuid %s, DRBDResource expects %s", backingDev, diskUUID, statusUUID),
				v1alpha1.DRBDResourceCondConfiguredReasonForeignDiskDetected,
			)}}
		case diskUUID != "" && statusUUID == "":
			// Adopt from disk — proceed to attach.
		case diskUUID == "" && statusUUID != "":
			actions = append(actions, WriteDeviceUUIDAction{Minor: minor, BackingDev: backingDev, UUID: statusUUID})
		default:
			actions = append(actions, WriteDeviceUUIDAction{Minor: minor, BackingDev: backingDev, UUID: generateDeviceUUID()})
		}
		actions = append(actions, ApplyALAction{Minor: minor, BackingDev: backingDev})
	} else {
		uuid := statusUUID
		if uuid == "" {
			uuid = generateDeviceUUID()
		}
		actions = append(actions,
			CreateMetadataAction{Minor: minor, BackingDev: backingDev},
			WriteDeviceUUIDAction{Minor: minor, BackingDev: backingDev, UUID: uuid},
		)
	}

	actions = append(actions, AttachAction{
		Minor:    minor,
		LowerDev: backingDev,
		MetaDev:  backingDev,
		MetaIdx:  "internal",
	})
	return actions
}

// computeDiskActions handles all disk-related actions: detach, attach, and options.
func computeDiskActions(minor *uint, iState IntendedDRBDState, aState ActualDRBDState) (res DRBDActions) {
	actualDisk := ""
	if len(aState.Volumes()) > 0 {
		actualDisk = aState.Volumes()[0].BackingDisk()
	}
	intendedDisk := iState.BackingDisk()

	if actualDisk != "" && actualDisk != intendedDisk {
		res = append(res, DetachAction{Minor: minor})
		return
	}

	if actualDisk == "" && intendedDisk != "" && iState.Type() == v1alpha1.DRBDResourceTypeDiskful {
		res = append(res, computeAttachActions(aState, iState.StatusDeviceUUID(), minor, intendedDisk)...)
		res = append(res, computeDiskOptionsAction(minor, iState)...)
		return
	}

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

	action := ResourceOptionsAction{
		ResourceName:               resourceName,
		AutoPromote:                &autoPromote,
		OnNoQuorum:                 iState.OnNoQuorum(),
		OnNoDataAccessible:         iState.OnNoDataAccessible(),
		OnSuspendedPrimaryOutdated: iState.OnSuspendedPrimaryOutdated(),
		Quorum:                     &quorum,
		QuorumMinimumRedundancy:    &quorumMinRedundancy,
	}

	// Include quorum-dynamic-voters when flant extensions are available,
	// or when the intended value differs from the DRBD built-in default (true).
	// In the latter case drbdsetup will fail on a non-flant kernel, surfacing
	// the error instead of silently ignoring the requested setting.
	if drbdutils.FlantExtensionsSupported || !iState.QuorumDynamicVoters() {
		qdv := iState.QuorumDynamicVoters()
		action.QuorumDynamicVoters = &qdv
	}

	res = append(res, action)
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

	// Check quorum-dynamic-voters (flant extension).
	// On non-flant kernels, only include if intended differs from DRBD default (true)
	// so that drbdsetup surfaces the error instead of silently ignoring the setting.
	if drbdutils.FlantExtensionsSupported || !iState.QuorumDynamicVoters() {
		if iState.QuorumDynamicVoters() != aState.QuorumDynamicVoters() {
			qdv := iState.QuorumDynamicVoters()
			action.QuorumDynamicVoters = &qdv
			changed = true
		}
	}

	if changed {
		res = append(res, action)
	}
	return res
}

func computeDiskOptionsAction(minor *uint, iState IntendedDRBDState) (res DRBDActions) {
	discardZeroes := iState.DiscardZeroesIfAligned()
	rsDiscardGran := iState.RsDiscardGranularity()
	action := DiskOptionsAction{
		Minor:                  minor,
		DiscardZeroesIfAligned: &discardZeroes,
		RsDiscardGranularity:   &rsDiscardGran,
	}

	// Include non-voting when flant extensions are available,
	// or when the intended value differs from the DRBD built-in default (false).
	if drbdutils.FlantExtensionsSupported || iState.NonVoting() {
		nv := iState.NonVoting()
		action.NonVoting = &nv
	}

	res = append(res, action)
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

		// Check non-voting (flant extension).
		// On non-flant kernels, only include if intended differs from DRBD default (false)
		// so that drbdsetup surfaces the error instead of silently ignoring the setting.
		if drbdutils.FlantExtensionsSupported || iState.NonVoting() {
			if iState.NonVoting() != vol.NonVoting() {
				nv := iState.NonVoting()
				action.NonVoting = &nv
				changed = true
			}
		}

		if changed {
			res = append(res, action)
		}
	}
	return res
}

// computePeerDeviceOptionsAction emits a PeerDeviceOptionsAction when the intended
// bitmap setting diverges from the actual one. For Diskless peers bitmap must be off;
// for Diskful peers it stays at the DRBD default (on) and no action is emitted.
func computePeerDeviceOptionsAction(resourceName string, iPeer IntendedPeer, aPeer ActualPeer) (res DRBDActions) {
	if iPeer.Type() != v1alpha1.DRBDResourceTypeDiskless {
		return
	}

	actualBitmap := aPeer.Bitmap()
	if actualBitmap != nil && !*actualBitmap {
		return
	}

	bitmapOff := false
	res = append(res, PeerDeviceOptionsAction{
		ResourceName: resourceName,
		PeerNodeID:   iPeer.NodeID(),
		VolumeNr:     0,
		Bitmap:       &bitmapOff,
	})
	return
}

func computeNetOptionsAction(resourceName string, iPeer IntendedPeer, allowTwoPrimaries bool) (res DRBDActions) {
	allowRemoteRead := iPeer.AllowRemoteRead()
	verifyAlg := iPeer.VerifyAlg()

	res = append(res, NetOptionsAction{
		ResourceName:      resourceName,
		PeerNodeID:        iPeer.NodeID(),
		AllowTwoPrimaries: &allowTwoPrimaries,
		AllowRemoteRead:   &allowRemoteRead,
		VerifyAlg:         &verifyAlg,
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

	// Check verify-alg
	if iPeer.VerifyAlg() != aPeer.VerifyAlg() {
		verifyAlg := iPeer.VerifyAlg()
		action.VerifyAlg = &verifyAlg
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

	// Guard: do not delete the last established path on an active connection.
	// DRBD kernel rejects del-path in that case. The scanner will trigger a
	// new reconcile once the replacement path becomes established.
	connectionActive := aPeer != nil &&
		aPeer.ConnectionState() != v1alpha1.ConnectionStateStandAlone.String()

	removedKeys := make(map[string]struct{}, len(toRemove))
	for _, rp := range toRemove {
		removedKeys[pathKey(rp.LocalAddr(), rp.RemoteAddr())] = struct{}{}
	}
	establishedRetained := 0
	for _, ap := range actualPaths {
		if _, removed := removedKeys[pathKey(ap.LocalAddr(), ap.RemoteAddr())]; !removed && ap.Established() {
			establishedRetained++
		}
	}

	for _, aPath := range toRemove {
		if connectionActive && aPath.Established() && establishedRetained == 0 {
			continue
		}
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

// computeResizeAction computes resize action if the intended upper-device
// size (spec.size) is larger than the actual DRBD device size. Both values
// are upper-device sizes in bytes; the comparison is direct.
//
// If the backing device hasn't actually grown yet, drbdsetup returns
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

	if iState.Size() > actualVol.Size() {
		minor := uint(actualVol.Minor())
		res = append(res, ResizeAction{Minor: &minor, SizeBytes: iState.Size()})
	}

	return
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
