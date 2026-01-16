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

package scanner

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestCalculateSyncProgress_PercentFormat(t *testing.T) {
	// This test verifies that calculateSyncProgress correctly parses PercentInSync
	// formatted by copyStatusFields using fmt.Sprintf("%.2f", float64).
	testCases := []struct {
		name          string
		percentInSync float64
		wantContains  string
	}{
		{"zero", 0.0, "0.00%"},
		{"half", 50.0, "50.00%"},
		{"full", 100.0, "100.00%"},
		{"fractional", 75.55, "75.55%"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondInSyncType,
							Status: metav1.ConditionFalse,
						},
					},
					DRBD: &v1alpha1.DRBD{
						Status: &v1alpha1.DRBDStatus{
							Devices: []v1alpha1.DeviceStatus{
								{DiskState: v1alpha1.DiskStateInconsistent},
							},
							Connections: []v1alpha1.ConnectionStatus{
								{
									PeerDevices: []v1alpha1.PeerDeviceStatus{
										{
											ReplicationState: v1alpha1.ReplicationStateSyncTarget,
											// Format exactly as copyStatusFields does
											PercentInSync: fmt.Sprintf("%.2f", tc.percentInSync),
										},
									},
								},
							},
						},
					},
				},
			}

			result := calculateSyncProgress(rvr)
			if result != tc.wantContains {
				t.Errorf("calculateSyncProgress() = %q, want %q", result, tc.wantContains)
			}
		})
	}
}

func TestCalculateSyncProgress_InSyncTrue(t *testing.T) {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondInSyncType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	result := calculateSyncProgress(rvr)
	if result != "True" {
		t.Errorf("calculateSyncProgress() = %q, want %q", result, "True")
	}
}

func TestCalculateSyncProgress_Unknown(t *testing.T) {
	// No conditions set - Status initialized but empty (as in real usage)
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{},
	}

	result := calculateSyncProgress(rvr)
	if result != "Unknown" {
		t.Errorf("calculateSyncProgress() = %q, want %q", result, "Unknown")
	}
}

func TestCalculateSyncProgress_DiskState(t *testing.T) {
	// InSync=False, no active sync -> return DiskState
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondInSyncType,
					Status: metav1.ConditionFalse,
				},
			},
			DRBD: &v1alpha1.DRBD{
				Status: &v1alpha1.DRBDStatus{
					Devices: []v1alpha1.DeviceStatus{
						{DiskState: v1alpha1.DiskStateOutdated},
					},
					Connections: []v1alpha1.ConnectionStatus{
						{
							PeerDevices: []v1alpha1.PeerDeviceStatus{
								{
									ReplicationState: v1alpha1.ReplicationStateEstablished,
									PercentInSync:    "100.00",
								},
							},
						},
					},
				},
			},
		},
	}

	result := calculateSyncProgress(rvr)
	if result != "Outdated" {
		t.Errorf("calculateSyncProgress() = %q, want %q", result, "Outdated")
	}
}
