package suite

import (
	"fmt"
	"time"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupLLVs Provides LVMLogicalVolume objects (100Mi, Thick) created for each
// given LVG. Each LLV is named "e2e-drbdr-<testID>-<nodeName>" and is cleaned
// up after the test. The LLVs are waited until they reach the "Created" phase.
// Returns LLVs in the same order as the input lvgs.
func SetupLLVs(
	e *etesting.E,
	cl client.Client,
	lvgs []*snc.LVMVolumeGroup,
) []*snc.LVMLogicalVolume {
	if len(lvgs) == 0 {
		e.Fatal("require: expected lvgs to be non-empty")
	}

	testID := e.TestID()
	result := make([]*snc.LVMLogicalVolume, 0, len(lvgs))

	for _, lvg := range lvgs {
		nodeName := lvg.Spec.Local.NodeName
		llvName := fmt.Sprintf("e2e-drbdr-%s-%s", testID, nodeName)

		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: llvName,
			},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: llvName,
				Type:                  "Thick",
				Size:                  "100Mi",
				LVMVolumeGroupName:    lvg.Name,
			},
		}

		// Discover: check if LLV already exists.
		existing := &snc.LVMLogicalVolume{}
		err := cl.Get(e.Context(), client.ObjectKey{Name: llvName}, existing)
		if err == nil {
			// Already exists — verify it belongs to the expected LVG.
			if existing.Spec.LVMVolumeGroupName != lvg.Name {
				e.Fatalf("LLV %q already exists but belongs to LVG %q, expected %q",
					llvName, existing.Spec.LVMVolumeGroupName, lvg.Name)
			}
		} else {
			// Arrange: create the LLV.
			if err := cl.Create(e.Context(), llv); err != nil {
				e.Fatalf("creating LLV %q: %v", llvName, err)
			}
		}

		// Cleanup.
		e.Cleanup(func() {
			toDelete := &snc.LVMLogicalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: llvName,
				},
			}
			if err := cl.Delete(e.Context(), toDelete); err != nil {
				e.Errorf("deleting LLV %q: %v", llvName, err)
			}
		})

		// Assert: wait for LLV to be created/ready.
		waitForLLVPhase(e, cl, llvName, snc.PhaseCreated)

		result = append(result, llv)
	}

	return result
}

// waitForLLVPhase polls an LVMLogicalVolume until it reaches the expected phase.
func waitForLLVPhase(
	e *etesting.E,
	cl client.Client,
	name string,
	expectedPhase string,
) {
	deadline := time.Now().Add(defaultTimeout)

	for {
		llv := &snc.LVMLogicalVolume{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, llv); err != nil {
			e.Fatalf("getting LLV %q: %v", name, err)
		}

		if llv.Status != nil && llv.Status.Phase == expectedPhase {
			return
		}

		currentPhase := ""
		if llv.Status != nil {
			currentPhase = llv.Status.Phase
		}

		if time.Now().After(deadline) {
			e.Fatalf("timed out waiting for LLV %q phase %q; current phase: %q",
				name, expectedPhase, currentPhase)
		}

		time.Sleep(defaultPollInterval)
	}
}
