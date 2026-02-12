package agent

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/deckhouse/sds-replicated-volume/e2e/agent/internal/suite"
)

func TestDRBDResource(t *testing.T) {
	// Discover configuration from the environment.
	cfg := DiscoverConfig(t)

	// Initialize K8s client.
	cl := SetupClient(t)

	// Start agent pod log monitoring.
	SetupPodLogMonitor(t, cfg.AgentPods)

	// Discover existing nodes.
	nodeNames := make([]string, len(cfg.Nodes))
	for i, n := range cfg.Nodes {
		nodeNames[i] = n.Name
	}
	_ = SetupExistingNodes(t, cl, nodeNames)

	// Discover existing LVGs.
	lvgs := SetupExistingLVGs(t, cl, cfg.Nodes)

	// Create LLVs for diskful nodes.
	testID := shortID()
	llvs := SetupLLVs(t, cl, testID, lvgs)

	// Create DRBDResources (without peers).
	drbdrs := SetupDRBDResources(t, cl, testID, nodeNames, llvs)

	// --- Standalone DRBDResource tests (without peers) ---

	t.Run("StandaloneDRBDResourceProducesAddresses", func(t *testing.T) {
		for _, info := range drbdrs {
			drbdr := getDRBDResource(t, cl, info.Name)

			if len(drbdr.Status.Addresses) == 0 {
				t.Errorf("DRBDResource %q has no addresses", info.Name)
				continue
			}

			for _, addr := range drbdr.Status.Addresses {
				if addr.SystemNetworkName != "Internal" {
					t.Errorf("DRBDResource %q: unexpected systemNetworkName %q, want %q",
						info.Name, addr.SystemNetworkName, "Internal")
				}
				if net.ParseIP(addr.Address.IPv4) == nil {
					t.Errorf("DRBDResource %q: invalid IPv4 %q", info.Name, addr.Address.IPv4)
				}
				if addr.Address.Port == 0 {
					t.Errorf("DRBDResource %q: port is 0", info.Name)
				}
			}
		}
	})

	t.Run("StandaloneDRBDResourceBecomesConfigured", func(t *testing.T) {
		for _, info := range drbdrs {
			drbdr := getDRBDResource(t, cl, info.Name)
			assertCondition(t, drbdr, v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
		}
	})

	t.Run("StandaloneDiskfulResourceDiskIsUpToDate", func(t *testing.T) {
		for _, info := range drbdrs {
			drbdr := getDRBDResource(t, cl, info.Name)

			if drbdr.Status.DiskState != v1alpha1.DiskStateUpToDate {
				t.Errorf("DRBDResource %q: diskState = %q, want %q",
					info.Name, drbdr.Status.DiskState, v1alpha1.DiskStateUpToDate)
			}
		}
	})

	t.Run("StandaloneDRBDResourceActiveConfigurationMatchesSpec", func(t *testing.T) {
		for _, info := range drbdrs {
			drbdr := getDRBDResource(t, cl, info.Name)

			ac := drbdr.Status.ActiveConfiguration
			if ac == nil {
				t.Errorf("DRBDResource %q: activeConfiguration is nil", info.Name)
				continue
			}

			if ac.State != v1alpha1.DRBDResourceStateUp {
				t.Errorf("DRBDResource %q: activeConfiguration.state = %q, want %q",
					info.Name, ac.State, v1alpha1.DRBDResourceStateUp)
			}
			if ac.Role != v1alpha1.DRBDRoleSecondary {
				t.Errorf("DRBDResource %q: activeConfiguration.role = %q, want %q",
					info.Name, ac.Role, v1alpha1.DRBDRoleSecondary)
			}
			if ac.Type != v1alpha1.DRBDResourceTypeDiskful {
				t.Errorf("DRBDResource %q: activeConfiguration.type = %q, want %q",
					info.Name, ac.Type, v1alpha1.DRBDResourceTypeDiskful)
			}
			if ac.LVMLogicalVolumeName != info.LLVName {
				t.Errorf("DRBDResource %q: activeConfiguration.lvmLogicalVolumeName = %q, want %q",
					info.Name, ac.LVMLogicalVolumeName, info.LLVName)
			}
		}
	})

	// --- Peered DRBDResource tests ---

	t.Run("WithPeers", func(t *testing.T) {
		// Add peers to all DRBDResources.
		SetupDRBDResourcesPeered(t, cl, drbdrs)

		t.Run("PeeredDRBDResourcesConfigured", func(t *testing.T) {
			for _, info := range drbdrs {
				drbdr := getDRBDResource(t, cl, info.Name)
				assertCondition(t, drbdr, v1alpha1.DRBDResourceCondConfiguredType, metav1.ConditionTrue)
			}
		})

		t.Run("PeeredDRBDResourcesPeerConnected", func(t *testing.T) {
			for _, info := range drbdrs {
				drbdr := getDRBDResource(t, cl, info.Name)
				expectedPeerCount := len(drbdrs) - 1

				if len(drbdr.Status.Peers) != expectedPeerCount {
					t.Errorf("DRBDResource %q: got %d peers, want %d",
						info.Name, len(drbdr.Status.Peers), expectedPeerCount)
					continue
				}

				for _, peer := range drbdr.Status.Peers {
					if peer.ConnectionState != v1alpha1.ConnectionStateConnected {
						t.Errorf("DRBDResource %q: peer %q connectionState = %q, want %q",
							info.Name, peer.Name, peer.ConnectionState, v1alpha1.ConnectionStateConnected)
					}
				}
			}
		})

		t.Run("PeeredDRBDResourcesReplicationEstablished", func(t *testing.T) {
			for _, info := range drbdrs {
				drbdr := getDRBDResource(t, cl, info.Name)

				for _, peer := range drbdr.Status.Peers {
					if peer.Type != v1alpha1.DRBDResourceTypeDiskful {
						continue // Only check diskful peers
					}
					if peer.ReplicationState != v1alpha1.ReplicationStateEstablished {
						t.Errorf("DRBDResource %q: peer %q replicationState = %q, want %q",
							info.Name, peer.Name, peer.ReplicationState, v1alpha1.ReplicationStateEstablished)
					}
				}
			}
		})

		t.Run("PeeredDRBDResourcesPathsEstablished", func(t *testing.T) {
			for _, info := range drbdrs {
				drbdr := getDRBDResource(t, cl, info.Name)

				for _, peer := range drbdr.Status.Peers {
					if len(peer.Paths) == 0 {
						t.Errorf("DRBDResource %q: peer %q has no paths", info.Name, peer.Name)
						continue
					}
					for _, path := range peer.Paths {
						if !path.Established {
							t.Errorf("DRBDResource %q: peer %q path %q not established",
								info.Name, peer.Name, path.SystemNetworkName)
						}
					}
				}
			}
		})
	})
}

// getDRBDResource fetches a DRBDResource by name, failing the test on error.
func getDRBDResource(t *testing.T, cl client.Client, name string) *v1alpha1.DRBDResource {
	t.Helper()
	drbdr := &v1alpha1.DRBDResource{}
	if err := cl.Get(t.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
		t.Fatalf("getting DRBDResource %q: %v", name, err)
	}
	return drbdr
}

// assertCondition checks that a DRBDResource has the expected condition status.
func assertCondition(t *testing.T, drbdr *v1alpha1.DRBDResource, condType string, expectedStatus metav1.ConditionStatus) {
	t.Helper()
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type == condType {
			if cond.Status != expectedStatus {
				t.Errorf("DRBDResource %q: condition %q status = %q, want %q (reason: %s)",
					drbdr.Name, condType, cond.Status, expectedStatus, cond.Reason)
			}
			return
		}
	}
	t.Errorf("DRBDResource %q: condition %q not found", drbdr.Name, condType)
}

// shortID generates a short random identifier for test resource names.
func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}
