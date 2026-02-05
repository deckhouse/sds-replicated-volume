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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Mock implementations for testing

type mockIntendedState struct {
	isZero                     bool
	isUpAndNotInCleanup        bool
	resourceName               string
	nodeID                     uint8
	resourceType               v1alpha1.DRBDResourceType
	backingDisk                string
	quorum                     byte
	quorumMinimumRedundancy    byte
	allowTwoPrimaries          bool
	role                       v1alpha1.DRBDRole
	size                       int64
	peers                      []IntendedPeer
	autoPromote                bool
	onNoQuorum                 string
	onNoDataAccessible         string
	onSuspendedPrimaryOutdated string
	discardZeroesIfAligned     bool
	rsDiscardGranularity       uint
}

func (m *mockIntendedState) IsZero() bool                       { return m.isZero }
func (m *mockIntendedState) IsUpAndNotInCleanup() bool          { return m.isUpAndNotInCleanup }
func (m *mockIntendedState) ResourceName() string               { return m.resourceName }
func (m *mockIntendedState) NodeID() uint8                      { return m.nodeID }
func (m *mockIntendedState) Type() v1alpha1.DRBDResourceType    { return m.resourceType }
func (m *mockIntendedState) BackingDisk() string                { return m.backingDisk }
func (m *mockIntendedState) Quorum() byte                       { return m.quorum }
func (m *mockIntendedState) QuorumMinimumRedundancy() byte      { return m.quorumMinimumRedundancy }
func (m *mockIntendedState) AllowTwoPrimaries() bool            { return m.allowTwoPrimaries }
func (m *mockIntendedState) Role() v1alpha1.DRBDRole            { return m.role }
func (m *mockIntendedState) Size() int64                        { return m.size }
func (m *mockIntendedState) Peers() []IntendedPeer              { return m.peers }
func (m *mockIntendedState) AutoPromote() bool                  { return m.autoPromote }
func (m *mockIntendedState) OnNoQuorum() string                 { return m.onNoQuorum }
func (m *mockIntendedState) OnNoDataAccessible() string         { return m.onNoDataAccessible }
func (m *mockIntendedState) OnSuspendedPrimaryOutdated() string { return m.onSuspendedPrimaryOutdated }
func (m *mockIntendedState) DiscardZeroesIfAligned() bool       { return m.discardZeroesIfAligned }
func (m *mockIntendedState) RsDiscardGranularity() uint         { return m.rsDiscardGranularity }

var _ IntendedDRBDState = (*mockIntendedState)(nil)

type mockActualState struct {
	isZero                     bool
	resourceName               string
	resourceExists             bool
	nodeID                     uint
	role                       string
	autoPromote                bool
	quorum                     string
	quorumMinimumRedundancy    string
	onNoQuorum                 string
	onNoDataAccessible         string
	onSuspendedPrimaryOutdated string
	volumes                    []ActualVolume
	peers                      []ActualPeer
}

func (m *mockActualState) IsZero() bool                       { return m.isZero }
func (m *mockActualState) ResourceName() string               { return m.resourceName }
func (m *mockActualState) ResourceExists() bool               { return m.resourceExists }
func (m *mockActualState) NodeID() uint                       { return m.nodeID }
func (m *mockActualState) Role() string                       { return m.role }
func (m *mockActualState) AutoPromote() bool                  { return m.autoPromote }
func (m *mockActualState) Quorum() string                     { return m.quorum }
func (m *mockActualState) QuorumMinimumRedundancy() string    { return m.quorumMinimumRedundancy }
func (m *mockActualState) OnNoQuorum() string                 { return m.onNoQuorum }
func (m *mockActualState) OnNoDataAccessible() string         { return m.onNoDataAccessible }
func (m *mockActualState) OnSuspendedPrimaryOutdated() string { return m.onSuspendedPrimaryOutdated }
func (m *mockActualState) Volumes() []ActualVolume            { return m.volumes }
func (m *mockActualState) Peers() []ActualPeer                { return m.peers }
func (m *mockActualState) Report(_ *v1alpha1.DRBDResource) error {
	return nil
}

var _ ActualDRBDState = (*mockActualState)(nil)

type mockActualVolume struct {
	minor                  int
	volumeNr               int
	backingDisk            string
	diskState              string
	hasQuorum              bool
	size                   int64
	discardZeroesIfAligned bool
	rsDiscardGranularity   string
}

func (m *mockActualVolume) Minor() int                   { return m.minor }
func (m *mockActualVolume) VolumeNr() int                { return m.volumeNr }
func (m *mockActualVolume) BackingDisk() string          { return m.backingDisk }
func (m *mockActualVolume) DiskState() string            { return m.diskState }
func (m *mockActualVolume) HasQuorum() bool              { return m.hasQuorum }
func (m *mockActualVolume) Size() int64                  { return m.size }
func (m *mockActualVolume) DiscardZeroesIfAligned() bool { return m.discardZeroesIfAligned }
func (m *mockActualVolume) RsDiscardGranularity() string { return m.rsDiscardGranularity }

var _ ActualVolume = (*mockActualVolume)(nil)

// Helper functions for creating test states

func newMockIntendedDiskful(resourceName, backingDisk string) *mockIntendedState {
	return &mockIntendedState{
		isUpAndNotInCleanup:        true,
		resourceName:               resourceName,
		nodeID:                     1,
		resourceType:               v1alpha1.DRBDResourceTypeDiskful,
		backingDisk:                backingDisk,
		onNoQuorum:                 "suspend-io",
		onNoDataAccessible:         "suspend-io",
		onSuspendedPrimaryOutdated: "force-secondary",
		rsDiscardGranularity:       8192,
		role:                       v1alpha1.DRBDRoleSecondary,
	}
}

func newMockIntendedDiskless(resourceName string) *mockIntendedState {
	return &mockIntendedState{
		isUpAndNotInCleanup:        true,
		resourceName:               resourceName,
		nodeID:                     1,
		resourceType:               v1alpha1.DRBDResourceTypeDiskless,
		backingDisk:                "",
		onNoQuorum:                 "suspend-io",
		onNoDataAccessible:         "suspend-io",
		onSuspendedPrimaryOutdated: "force-secondary",
		rsDiscardGranularity:       8192,
		role:                       v1alpha1.DRBDRoleSecondary,
	}
}

func newMockActualNotExists() *mockActualState {
	return &mockActualState{
		resourceExists: false,
	}
}

func newMockActualExistsNoVolumes(resourceName string) *mockActualState {
	return &mockActualState{
		resourceExists:             true,
		resourceName:               resourceName,
		nodeID:                     1,
		onNoQuorum:                 "suspend-io",
		onNoDataAccessible:         "suspend-io",
		onSuspendedPrimaryOutdated: "force-secondary",
		volumes:                    nil,
	}
}

func newMockActualExistsWithDisklessVolume(resourceName string, minor int) *mockActualState {
	return &mockActualState{
		resourceExists:             true,
		resourceName:               resourceName,
		nodeID:                     1,
		onNoQuorum:                 "suspend-io",
		onNoDataAccessible:         "suspend-io",
		onSuspendedPrimaryOutdated: "force-secondary",
		volumes: []ActualVolume{
			&mockActualVolume{
				minor:       minor,
				volumeNr:    0,
				backingDisk: "",
				diskState:   "Diskless",
			},
		},
	}
}

func newMockActualExistsWithDisk(resourceName string, minor int, backingDisk string) *mockActualState {
	return &mockActualState{
		resourceExists:             true,
		resourceName:               resourceName,
		nodeID:                     1,
		onNoQuorum:                 "suspend-io",
		onNoDataAccessible:         "suspend-io",
		onSuspendedPrimaryOutdated: "force-secondary",
		volumes: []ActualVolume{
			&mockActualVolume{
				minor:                minor,
				volumeNr:             0,
				backingDisk:          backingDisk,
				diskState:            "UpToDate",
				rsDiscardGranularity: "8192",
			},
		},
	}
}

// hasAction checks if actions contain the specified action type
func hasAction[T any](actions DRBDActions) bool {
	for _, a := range actions {
		if _, ok := a.(T); ok {
			return true
		}
	}
	return false
}

// Tests

func TestComputeBringUpActions_NewResource_Diskful(t *testing.T) {
	// When: resource doesn't exist, intended is diskful with backing disk
	// Then: should create resource, minor, metadata, attach, and set options
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv")
	aState := newMockActualNotExists()

	actions := computeBringUpActions(iState, aState)

	assert.True(t, hasAction[NewResourceAction](actions), "should create resource")
	assert.True(t, hasAction[NewMinorAction](actions), "should create minor")
	assert.True(t, hasAction[CreateMetadataAction](actions), "should create metadata")
	assert.True(t, hasAction[AttachAction](actions), "should attach disk")
	assert.True(t, hasAction[ResourceOptionsAction](actions), "should set resource options")
	assert.True(t, hasAction[DiskOptionsAction](actions), "should set disk options")
}

func TestComputeBringUpActions_NewResource_Diskless(t *testing.T) {
	// When: resource doesn't exist, intended is diskless
	// Then: should create resource and minor, but NOT attach disk
	iState := newMockIntendedDiskless("test-res")
	aState := newMockActualNotExists()

	actions := computeBringUpActions(iState, aState)

	assert.True(t, hasAction[NewResourceAction](actions), "should create resource")
	assert.True(t, hasAction[NewMinorAction](actions), "should create minor")
	assert.False(t, hasAction[CreateMetadataAction](actions), "should NOT create metadata for diskless")
	assert.False(t, hasAction[AttachAction](actions), "should NOT attach disk for diskless")
}

func TestComputeBringUpActions_ExistingResource_NoVolumes_Diskful(t *testing.T) {
	// Bug fix test: When resource exists but has no volumes, should create minor and attach
	// This scenario can happen if volume was manually deleted
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv")
	aState := newMockActualExistsNoVolumes("test-res")

	actions := computeBringUpActions(iState, aState)

	assert.False(t, hasAction[NewResourceAction](actions), "should NOT create resource (already exists)")
	assert.True(t, hasAction[NewMinorAction](actions), "should create minor")
	assert.True(t, hasAction[CreateMetadataAction](actions), "should create metadata")
	assert.True(t, hasAction[AttachAction](actions), "should attach disk")
}

func TestComputeBringUpActions_ExistingResource_DisklessVolume_AttachLater(t *testing.T) {
	// Bug fix test: When resource exists with diskless volume, and we add lvmLogicalVolumeName later
	// This is the primary bug reported by the user
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv")
	aState := newMockActualExistsWithDisklessVolume("test-res", 1000)

	actions := computeBringUpActions(iState, aState)

	assert.False(t, hasAction[NewResourceAction](actions), "should NOT create resource (already exists)")
	assert.False(t, hasAction[NewMinorAction](actions), "should NOT create minor (already exists)")
	assert.True(t, hasAction[CreateMetadataAction](actions), "should create metadata for late attach")
	assert.True(t, hasAction[AttachAction](actions), "should attach disk for late attach")
	assert.True(t, hasAction[DiskOptionsAction](actions), "should set disk options after attach")
}

func TestComputeBringUpActions_ExistingResource_DiskAttached_NoChange(t *testing.T) {
	// When: resource exists with disk attached, same as intended
	// Then: should NOT detach or attach (no-op for disk)
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv")
	aState := newMockActualExistsWithDisk("test-res", 1000, "/dev/vg/lv")

	actions := computeBringUpActions(iState, aState)

	assert.False(t, hasAction[NewResourceAction](actions), "should NOT create resource")
	assert.False(t, hasAction[NewMinorAction](actions), "should NOT create minor")
	assert.False(t, hasAction[DetachAction](actions), "should NOT detach (same disk)")
	assert.False(t, hasAction[AttachAction](actions), "should NOT attach (already attached)")
}

func TestComputeBringUpActions_ExistingResource_DetachOnDiskChange(t *testing.T) {
	// When: resource exists with disk A, intended disk is B
	// Then: should detach (attach will happen on next reconcile cycle)
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv-new")
	aState := newMockActualExistsWithDisk("test-res", 1000, "/dev/vg/lv-old")

	actions := computeBringUpActions(iState, aState)

	assert.True(t, hasAction[DetachAction](actions), "should detach old disk")
	assert.False(t, hasAction[AttachAction](actions), "should NOT attach in same cycle as detach")
}

func TestComputeBringUpActions_ExistingResource_DetachOnGoDiskless(t *testing.T) {
	// When: resource exists with disk, intended is diskless
	// Then: should detach
	iState := newMockIntendedDiskless("test-res")
	aState := newMockActualExistsWithDisk("test-res", 1000, "/dev/vg/lv")

	actions := computeBringUpActions(iState, aState)

	assert.True(t, hasAction[DetachAction](actions), "should detach when going diskless")
	assert.False(t, hasAction[AttachAction](actions), "should NOT attach when going diskless")
}

func TestComputeMinorActions_VolumeExists(t *testing.T) {
	// When: volume already exists
	// Then: should return existing minor pointer, no actions
	aState := newMockActualExistsWithDisk("test-res", 1000, "/dev/vg/lv")
	var allocatedMinor uint

	minor, actions := computeMinorActions("test-res", &allocatedMinor, aState)

	assert.NotNil(t, minor, "should return minor pointer")
	assert.Equal(t, uint(1000), *minor, "should return existing minor")
	assert.Empty(t, actions, "should have no actions")
}

func TestComputeMinorActions_NoVolumes(t *testing.T) {
	// When: no volumes exist
	// Then: should return allocatedMinor pointer and NewMinorAction
	aState := newMockActualExistsNoVolumes("test-res")
	var allocatedMinor uint

	minor, actions := computeMinorActions("test-res", &allocatedMinor, aState)

	assert.Equal(t, &allocatedMinor, minor, "should return allocatedMinor pointer")
	assert.Len(t, actions, 1, "should have 1 action")
	assert.True(t, hasAction[NewMinorAction](actions), "should have NewMinorAction")
}

func TestComputeDiskActions_AttachWhenDiskless(t *testing.T) {
	// When: volume is diskless, intended is diskful
	// Then: should attach
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv")
	aState := newMockActualExistsWithDisklessVolume("test-res", 1000)
	minor := uint(1000)

	actions := computeDiskActions(&minor, iState, aState)

	assert.True(t, hasAction[CreateMetadataAction](actions), "should create metadata")
	assert.True(t, hasAction[AttachAction](actions), "should attach")
	assert.True(t, hasAction[DiskOptionsAction](actions), "should set disk options")
}

func TestComputeDiskActions_DetachWhenChanging(t *testing.T) {
	// When: disk is different
	// Then: should detach only (attach on next cycle)
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv-new")
	aState := newMockActualExistsWithDisk("test-res", 1000, "/dev/vg/lv-old")
	minor := uint(1000)

	actions := computeDiskActions(&minor, iState, aState)

	assert.True(t, hasAction[DetachAction](actions), "should detach")
	assert.False(t, hasAction[AttachAction](actions), "should NOT attach in same cycle")
}

func TestComputeDiskActions_NoActionWhenSame(t *testing.T) {
	// When: disk is same as intended
	// Then: should only reconcile options (no attach/detach)
	iState := newMockIntendedDiskful("test-res", "/dev/vg/lv")
	aState := newMockActualExistsWithDisk("test-res", 1000, "/dev/vg/lv")
	minor := uint(1000)

	actions := computeDiskActions(&minor, iState, aState)

	assert.False(t, hasAction[DetachAction](actions), "should NOT detach")
	assert.False(t, hasAction[AttachAction](actions), "should NOT attach")
	// Disk options reconciliation may or may not add actions depending on current state
}
