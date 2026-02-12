package suite

import (
	"fmt"
	"testing"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LLVInfo holds a created LVMLogicalVolume and its associated node.
type LLVInfo struct {
	NodeName string
	LLVName  string
	LVGName  string
}

// SetupLLVs Provides LVMLogicalVolume objects (100Mi, Thick) created for each
// LVG in the given lvgInfos. Each LLV is named "e2e-drbdr-<nodeName>" and is
// cleaned up after the test. The LLVs are waited until they reach the "Created"
// phase.
func SetupLLVs(
	t *testing.T,
	cl client.Client,
	testID string,
	lvgInfos []LVGInfo,
) []LLVInfo {
	if len(lvgInfos) == 0 {
		t.Fatal("expected lvgInfos to be non-empty")
	}

	result := make([]LLVInfo, 0, len(lvgInfos))

	for _, lvgInfo := range lvgInfos {
		llvName := fmt.Sprintf("e2e-drbdr-%s-%s", testID, lvgInfo.NodeName)

		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: llvName,
			},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: llvName,
				Type:                  "Thick",
				Size:                  "100Mi",
				LVMVolumeGroupName:    lvgInfo.LVGName,
			},
		}

		// Discover: check if LLV already exists
		existing := &snc.LVMLogicalVolume{}
		err := cl.Get(t.Context(), client.ObjectKey{Name: llvName}, existing)
		if err == nil {
			// Already exists - verify it belongs to the expected LVG
			if existing.Spec.LVMVolumeGroupName != lvgInfo.LVGName {
				t.Fatalf("LLV %q already exists but belongs to LVG %q, expected %q",
					llvName, existing.Spec.LVMVolumeGroupName, lvgInfo.LVGName)
			}
		} else {
			// Arrange: create the LLV
			if err := cl.Create(t.Context(), llv); err != nil {
				t.Fatalf("creating LLV %q: %v", llvName, err)
			}
		}

		// Cleanup
		t.Cleanup(func() {
			toDelete := &snc.LVMLogicalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: llvName,
				},
			}
			if err := cl.Delete(t.Context(), toDelete); err != nil {
				t.Errorf("deleting LLV %q: %v", llvName, err)
			}
		})

		// Assert: wait for LLV to be created/ready
		waitForLLVPhase(t, cl, llvName, snc.PhaseCreated)

		result = append(result, LLVInfo{
			NodeName: lvgInfo.NodeName,
			LLVName:  llvName,
			LVGName:  lvgInfo.LVGName,
		})
	}

	return result
}

// waitForLLVPhase polls an LVMLogicalVolume until it reaches the expected phase.
func waitForLLVPhase(
	t *testing.T,
	cl client.Client,
	name string,
	expectedPhase string,
) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)

	for {
		llv := &snc.LVMLogicalVolume{}
		if err := cl.Get(t.Context(), client.ObjectKey{Name: name}, llv); err != nil {
			t.Fatalf("getting LLV %q: %v", name, err)
		}

		if llv.Status != nil && llv.Status.Phase == expectedPhase {
			return
		}

		currentPhase := ""
		if llv.Status != nil {
			currentPhase = llv.Status.Phase
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for LLV %q phase %q; current phase: %q",
				name, expectedPhase, currentPhase)
		}

		time.Sleep(defaultPollInterval)
	}
}
