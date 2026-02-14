package agent

import (
	"net"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
)

func TestDRBDResource(t *testing.T) {
	e := etesting.New(t)

	// Discover K8s client.
	cl := DiscoverClient(e)

	// Start agent pod log monitoring.
	SetupPodLogMonitor(e)

	// Set up DRBDResources (discovers nodes, LVGs, creates LLVs and DRBDResources).
	drbdrs := SetupDRBDResources(e, cl)

	// --- Standalone DRBDResource tests (without peers) ---

	e.Run("StandaloneDRBDResourceProducesAddresses", func(e *etesting.E) {
		for _, dr := range drbdrs {
			drbdr := getDRBDResource(e, cl, dr.Name)

			if len(drbdr.Status.Addresses) == 0 {
				e.Errorf("DRBDResource %q has no addresses", drbdr.Name)
				continue
			}

			for _, addr := range drbdr.Status.Addresses {
				if addr.SystemNetworkName != "Internal" {
					e.Errorf("DRBDResource %q: unexpected systemNetworkName %q, want %q",
						drbdr.Name, addr.SystemNetworkName, "Internal")
				}
				if net.ParseIP(addr.Address.IPv4) == nil {
					e.Errorf("DRBDResource %q: invalid IPv4 %q", drbdr.Name, addr.Address.IPv4)
				}
				if addr.Address.Port == 0 {
					e.Errorf("DRBDResource %q: port is 0", drbdr.Name)
				}
			}
		}
	})

	e.Run("StandaloneDRBDResourceBecomesConfigured", func(e *etesting.E) {
		for _, dr := range drbdrs {
			drbdr := getDRBDResource(e, cl, dr.Name)
			assertCondition(e, drbdr, v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
		}
	})

	e.Run("StandaloneDiskfulResourceDiskIsUpToDate", func(e *etesting.E) {
		for _, dr := range drbdrs {
			drbdr := getDRBDResource(e, cl, dr.Name)

			if drbdr.Status.DiskState != v1alpha1.DiskStateUpToDate {
				e.Errorf("DRBDResource %q: diskState = %q, want %q",
					drbdr.Name, drbdr.Status.DiskState, v1alpha1.DiskStateUpToDate)
			}
		}
	})

	e.Run("StandaloneDRBDResourceActiveConfigurationMatchesSpec", func(e *etesting.E) {
		for _, dr := range drbdrs {
			drbdr := getDRBDResource(e, cl, dr.Name)

			ac := drbdr.Status.ActiveConfiguration
			if ac == nil {
				e.Errorf("DRBDResource %q: activeConfiguration is nil", drbdr.Name)
				continue
			}

			if ac.State != v1alpha1.DRBDResourceStateUp {
				e.Errorf("DRBDResource %q: activeConfiguration.state = %q, want %q",
					drbdr.Name, ac.State, v1alpha1.DRBDResourceStateUp)
			}
			if ac.Role != v1alpha1.DRBDRoleSecondary {
				e.Errorf("DRBDResource %q: activeConfiguration.role = %q, want %q",
					drbdr.Name, ac.Role, v1alpha1.DRBDRoleSecondary)
			}
			if ac.Type != v1alpha1.DRBDResourceTypeDiskful {
				e.Errorf("DRBDResource %q: activeConfiguration.type = %q, want %q",
					drbdr.Name, ac.Type, v1alpha1.DRBDResourceTypeDiskful)
			}
			if ac.LVMLogicalVolumeName != dr.Spec.LVMLogicalVolumeName {
				e.Errorf("DRBDResource %q: activeConfiguration.lvmLogicalVolumeName = %q, want %q",
					drbdr.Name, ac.LVMLogicalVolumeName, dr.Spec.LVMLogicalVolumeName)
			}
		}
	})

	// --- Peered DRBDResource tests ---

	e.Run("WithPeers", func(e *etesting.E) {
		// Add peers to all DRBDResources.
		SetupDRBDResourcesPeered(e, cl, drbdrs)

		e.Run("PeeredDRBDResourcesConfigured", func(e *etesting.E) {
			for _, dr := range drbdrs {
				drbdr := getDRBDResource(e, cl, dr.Name)
				assertCondition(e, drbdr, v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
			}
		})

		e.Run("PeeredDRBDResourcesPeerConnected", func(e *etesting.E) {
			for _, dr := range drbdrs {
				drbdr := getDRBDResource(e, cl, dr.Name)
				expectedPeerCount := len(drbdrs) - 1

				if len(drbdr.Status.Peers) != expectedPeerCount {
					e.Errorf("DRBDResource %q: got %d peers, want %d",
						drbdr.Name, len(drbdr.Status.Peers), expectedPeerCount)
					continue
				}

				for _, peer := range drbdr.Status.Peers {
					if peer.ConnectionState != v1alpha1.ConnectionStateConnected {
						e.Errorf("DRBDResource %q: peer %q connectionState = %q, want %q",
							drbdr.Name, peer.Name, peer.ConnectionState, v1alpha1.ConnectionStateConnected)
					}
				}
			}
		})

		e.Run("PeeredDRBDResourcesReplicationEstablished", func(e *etesting.E) {
			for _, dr := range drbdrs {
				drbdr := getDRBDResource(e, cl, dr.Name)

				for _, peer := range drbdr.Status.Peers {
					if peer.Type != v1alpha1.DRBDResourceTypeDiskful {
						continue // Only check diskful peers.
					}
					if peer.ReplicationState != v1alpha1.ReplicationStateEstablished {
						e.Errorf("DRBDResource %q: peer %q replicationState = %q, want %q",
							drbdr.Name, peer.Name, peer.ReplicationState, v1alpha1.ReplicationStateEstablished)
					}
				}
			}
		})

		e.Run("PeeredDRBDResourcesPathsEstablished", func(e *etesting.E) {
			for _, dr := range drbdrs {
				drbdr := getDRBDResource(e, cl, dr.Name)

				for _, peer := range drbdr.Status.Peers {
					if len(peer.Paths) == 0 {
						e.Errorf("DRBDResource %q: peer %q has no paths", drbdr.Name, peer.Name)
						continue
					}
					for _, path := range peer.Paths {
						if !path.Established {
							e.Errorf("DRBDResource %q: peer %q path %q not established",
								drbdr.Name, peer.Name, path.SystemNetworkName)
						}
					}
				}
			}
		})
	})
}

// getDRBDResource fetches a DRBDResource by name, failing the test on error.
func getDRBDResource(e *etesting.E, cl client.Client, name string) *v1alpha1.DRBDResource {
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		e.Fatalf("getting DRBDResource %q: %v", name, err)
	}
	return drbdr
}

// assertCondition checks that a DRBDResource has the expected condition status.
func assertCondition(e *etesting.E, drbdr *v1alpha1.DRBDResource, condType string, expectedStatus metav1.ConditionStatus) {
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type == condType {
			if cond.Status != expectedStatus {
				e.Errorf("DRBDResource %q: condition %q status = %q, want %q (reason: %s)",
					drbdr.Name, condType, cond.Status, expectedStatus, cond.Reason)
			}
			return
		}
	}
	e.Errorf("DRBDResource %q: condition %q not found", drbdr.Name, condType)
}
