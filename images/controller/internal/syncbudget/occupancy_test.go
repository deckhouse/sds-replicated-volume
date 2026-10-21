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

package syncbudget_test

import (
	"maps"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
)

// newRVR builds a replica carrying only what the occupancy sweep reads: its node, its
// type, its disk state and the replication state of each of its peers. An empty disk
// state means the replica reports no backing volume at all.
func newRVR(
	node string,
	replicaType v1alpha1.ReplicaType,
	disk v1alpha1.DiskState,
	peers ...v1alpha1.ReplicationState,
) *v1alpha1.ReplicatedVolumeReplica {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			NodeName: node,
			Type:     replicaType,
		},
	}
	if disk != "" {
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: disk}
	}
	for i, state := range peers {
		rvr.Status.Peers = append(rvr.Status.Peers, v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			Name:             "rv-test-" + string(rune('0'+i)),
			ReplicationState: state,
		})
	}
	return rvr
}

// ShadowDiskful is the type that separates load from redundancy: it pulls
// its copy over the network like a Diskful but does not vote, so both occupancy signals must see
// it — including the safety branch, which fires before DRBD reports anything.
func TestComputeActualOccupancyShadowDiskful(t *testing.T) {
	syncing := []*v1alpha1.ReplicatedVolumeReplica{
		newRVR("node-a", v1alpha1.ReplicaTypeShadowDiskful, v1alpha1.DiskStateInconsistent,
			v1alpha1.ReplicationStateSyncTarget),
	}
	if got, want := syncbudget.ComputeActualOccupancy(syncing), map[string]int{"node-a": 1}; !maps.Equal(got, want) {
		t.Errorf("shadow diskful with a syncing peer = %v, want %v", got, want)
	}

	quiet := []*v1alpha1.ReplicatedVolumeReplica{
		newRVR("node-a", v1alpha1.ReplicaTypeShadowDiskful, v1alpha1.DiskStateInconsistent,
			v1alpha1.ReplicationStateEstablished),
	}
	if got, want := syncbudget.ComputeActualOccupancy(quiet), map[string]int{"node-a": 1}; !maps.Equal(got, want) {
		t.Errorf("shadow diskful owed data with no peer syncing = %v, want %v (the load criterion)", got, want)
	}
}

func TestPromotionOccupancyStartsAtStorageAdmission(t *testing.T) {
	rvr := newRVR("node-tb", v1alpha1.ReplicaTypeDiskful, "")
	rvr.Status.DatameshRevision = 10
	rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskless
	if syncbudget.OccupiesEnds(rvr) {
		t.Fatal("a diskless member without storage must not block its own receiver admission")
	}
	rvr.Spec.LVMVolumeGroupName = "admitted-vg"
	if !syncbudget.OccupiesEnds(rvr) {
		t.Fatal("storage admission must reserve the receiver before resync starts")
	}
	rvr.Spec.LVMVolumeGroupName = ""
	rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{ReplicationState: v1alpha1.ReplicationStateSyncSource}}
	if !syncbudget.OccupiesEnds(rvr) {
		t.Fatal("a reported transfer must remain charged regardless of preparation state")
	}
}

func TestComputeActualOccupancy(t *testing.T) {
	tests := []struct {
		name string
		rvrs []*v1alpha1.ReplicatedVolumeReplica
		want map[string]int
	}{
		{
			name: "one replica pulling from one peer — one end on its node",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateInconsistent,
					v1alpha1.ReplicationStateSyncTarget),
			},
			want: map[string]int{"node-a": 1},
		},
		{
			// A replica with no node yet occupies nothing: there is no node to charge.
			// It must not leave an empty-string key behind either.
			name: "replica not placed on a node — occupies nothing",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateInconsistent),
			},
			want: map[string]int{},
		},
		{
			// The safety branch: a Diskful whose disk is not UpToDate is owed data, so it
			// holds an end even while no peer reports syncing.
			name: "diskful owed data but no peer syncing — safety branch holds an end",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateInconsistent,
					v1alpha1.ReplicationStateEstablished),
			},
			want: map[string]int{"node-a": 1},
		},
		{
			// An UpToDate replica with a settled connection owes nothing and is owed
			// nothing. Its node must be absent from the map, not present with a zero.
			name: "replica up to date and settled — node absent from the map",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateUpToDate,
					v1alpha1.ReplicationStateEstablished),
			},
			want: map[string]int{},
		},
		{
			// Fan-out: one node feeding two targets reads its disk twice over, so it
			// carries two ends, not one.
			name: "source feeding two targets — two ends on one node",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateUpToDate,
					v1alpha1.ReplicationStateSyncSource, v1alpha1.ReplicationStateSyncSource),
			},
			want: map[string]int{"node-a": 2},
		},
		{
			// A paused sync burns neither network nor disk, yet it holds its end. Two of
			// them also prove the count came from the peer states, not the safety branch.
			name: "paused syncs still hold their ends",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateInconsistent,
					v1alpha1.ReplicationStatePausedSyncTarget, v1alpha1.ReplicationStatePausedSyncSource),
			},
			want: map[string]int{"node-a": 2},
		},
		{
			// A tie-breaker is a vote without data, so it is owed nothing and the safety
			// branch must not fire for it even though it reports no backing volume.
			name: "tie-breaker is owed no data — occupies nothing",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeTieBreaker, "",
					v1alpha1.ReplicationStateEstablished),
			},
			want: map[string]int{},
		},
		{
			// A Diskful that reports no backing volume at all is owed data just as much
			// as one reporting Inconsistent.
			name: "diskful with no backing volume reported — safety branch holds an end",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, ""),
			},
			want: map[string]int{"node-a": 1},
		},
		{
			// Occupancy is a property of the node, not of a volume: replicas of different
			// volumes that landed on one node add up.
			name: "two replicas of different volumes on one node — ends add up",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateInconsistent,
					v1alpha1.ReplicationStateSyncTarget),
				newRVR("node-a", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateInconsistent,
					v1alpha1.ReplicationStateSyncTarget),
				newRVR("node-b", v1alpha1.ReplicaTypeDiskful, v1alpha1.DiskStateUpToDate,
					v1alpha1.ReplicationStateSyncSource),
			},
			want: map[string]int{"node-a": 2, "node-b": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncbudget.ComputeActualOccupancy(tt.rvrs); !maps.Equal(got, tt.want) {
				t.Errorf("ComputeActualOccupancy() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The safety branch assumes a transfer is coming, so it must stop where the transfer cannot: a
// replica on its way out will never take the data, and one whose every connection is down has no
// path for it to arrive by. Otherwise a replica stuck forever holds its node's budget forever.
//
// It stops only the assumption. The reported DRBD signal is the precise one and survives both, so a
// replica that really is pulling stays charged either way.
func TestComputeActualOccupancySafetyBranchStops(t *testing.T) {
	// deleting stamps rvr the way the API server does when a delete lands on it.
	deleting := func(rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.ReplicatedVolumeReplica {
		rvr.DeletionTimestamp = &metav1.Time{Time: time.Unix(1, 0)}
		return rvr
	}

	// connected sets the connection state of rvr's peers, in the order newRVR created them.
	connected := func(
		rvr *v1alpha1.ReplicatedVolumeReplica,
		states ...v1alpha1.ConnectionState,
	) *v1alpha1.ReplicatedVolumeReplica {
		for i, state := range states {
			rvr.Status.Peers[i].ConnectionState = state
		}
		return rvr
	}

	tests := []struct {
		name string
		rvr  *v1alpha1.ReplicatedVolumeReplica
		want map[string]int
	}{
		{
			name: "diskful owed data on its way out — no end held",
			rvr: deleting(newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent)),
			want: map[string]int{},
		},
		{
			// The deletion may well be blocked behind a finalizer while the kernel keeps copying.
			name: "diskful on its way out but really pulling — the reported end stands",
			rvr: deleting(newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent, v1alpha1.ReplicationStateSyncTarget)),
			want: map[string]int{"node-a": 1},
		},
		{
			name: "diskful owed data with its only connection down — no end held",
			rvr: connected(newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent, v1alpha1.ReplicationStateOff),
				v1alpha1.ConnectionStateStandAlone),
			want: map[string]int{},
		},
		{
			// One live connection is a path for the data, so the end is still owed.
			name: "diskful owed data with one connection down and one up — end held",
			rvr: connected(newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent,
				v1alpha1.ReplicationStateOff, v1alpha1.ReplicationStateEstablished),
				v1alpha1.ConnectionStateStandAlone, v1alpha1.ConnectionStateConnected),
			want: map[string]int{"node-a": 1},
		},
		{
			// An unset field is the agent not reporting it, not a dead connection: the charge
			// stays, which is the safe direction.
			name: "diskful owed data with an unreported connection state — end held",
			rvr: newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent, v1alpha1.ReplicationStateOff),
			want: map[string]int{"node-a": 1},
		},
		{
			name: "diskful owed data with an Unknown connection state — end held",
			rvr: connected(newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent, v1alpha1.ReplicationStateOff),
				v1alpha1.ConnectionStateUnknown),
			want: map[string]int{"node-a": 1},
		},
		{
			// The agent dying mid-transfer leaves the last known peer state behind, and while data
			// was flowing that state is a syncing one — so the reported signal alone still charges
			// the node, without the safety branch.
			name: "agent gone while the kernel copies — the last reported end stands",
			rvr: connected(newRVR("node-a", v1alpha1.ReplicaTypeDiskful,
				v1alpha1.DiskStateInconsistent, v1alpha1.ReplicationStateSyncTarget),
				v1alpha1.ConnectionStateConnected),
			want: map[string]int{"node-a": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rvrs := []*v1alpha1.ReplicatedVolumeReplica{tt.rvr}
			if got := syncbudget.ComputeActualOccupancy(rvrs); !maps.Equal(got, tt.want) {
				t.Errorf("ComputeActualOccupancy() = %v, want %v", got, tt.want)
			}
		})
	}
}
