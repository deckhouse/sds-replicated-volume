package agent

import (
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
	_ = cl

	// Start agent pod log monitoring.
	podsLogs := SetupPodsLogWatcher(e)
	SetupErrorLogsWatcher(e, podsLogs)

	e.Run("R1", func(e *etesting.E) {

	})

	e.Run("R2", func(e *etesting.E) {

	})

	e.Run("R3", func(e *etesting.E) {

	})

	e.Run("R4", func(e *etesting.E) {

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
