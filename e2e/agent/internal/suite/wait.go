package suite

import (
	"time"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultPollInterval = 2 * time.Second
	defaultTimeout      = 120 * time.Second
)

// waitForDRBDResourceCondition polls a DRBDResource until the given condition
// type has the expected status, or the timeout expires.
// Returns the last-observed DRBDResource.
func waitForDRBDResourceCondition(
	e *etesting.E,
	cl client.Client,
	name string,
	condType string,
	expectedStatus metav1.ConditionStatus,
) *v1alpha1.DRBDResource {
	deadline := time.Now().Add(defaultTimeout)

	for {
		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
			e.Fatalf("getting DRBDResource %q: %v", name, err)
		}

		for _, cond := range drbdr.Status.Conditions {
			if cond.Type == condType && cond.Status == expectedStatus {
				return drbdr
			}
		}

		if time.Now().After(deadline) {
			e.Fatalf("timed out waiting for DRBDResource %q condition %s=%s; last conditions: %v",
				name, condType, expectedStatus, formatConditions(drbdr.Status.Conditions))
		}

		time.Sleep(defaultPollInterval)
	}
}

// waitForDRBDResourceAddresses polls a DRBDResource until status.addresses is
// populated with at least one entry that has a non-zero port.
// Returns the last-observed DRBDResource.
func waitForDRBDResourceAddresses(
	e *etesting.E,
	cl client.Client,
	name string,
) *v1alpha1.DRBDResource {
	deadline := time.Now().Add(defaultTimeout)

	for {
		drbdr := &v1alpha1.DRBDResource{}
		if err := cl.Get(e.Context(), client.ObjectKey{Name: name}, drbdr); err != nil {
			e.Fatalf("getting DRBDResource %q: %v", name, err)
		}

		if hasValidAddresses(drbdr) {
			return drbdr
		}

		if time.Now().After(deadline) {
			e.Fatalf("timed out waiting for DRBDResource %q to have valid addresses; last addresses: %v",
				name, drbdr.Status.Addresses)
		}

		time.Sleep(defaultPollInterval)
	}
}

func hasValidAddresses(drbdr *v1alpha1.DRBDResource) bool {
	if len(drbdr.Status.Addresses) == 0 {
		return false
	}
	for _, addr := range drbdr.Status.Addresses {
		if addr.Address.IPv4 == "" || addr.Address.Port == 0 {
			return false
		}
	}
	return true
}

func formatConditions(conditions []metav1.Condition) string {
	if len(conditions) == 0 {
		return "(none)"
	}
	result := ""
	for i, c := range conditions {
		if i > 0 {
			result += ", "
		}
		result += c.Type + "=" + string(c.Status) + " (" + c.Reason + ")"
	}
	return result
}
