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
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

func TestEnsureLocalPortConflictResolved(t *testing.T) {
	makeConflictErr := func(ip string) error {
		inner := fmt.Errorf("running command drbdsetup new-path: %w",
			errors.Join(drbdutils.ErrNewPathLocalAddrInUse, fmt.Errorf("exit status 10")))
		return ConfiguredReasonError(
			&localPortConflictError{ip: ip, err: inner},
			v1alpha1.DRBDResourceCondConfiguredReasonNewPathFailed,
		)
	}

	makeDRBDR := func(ip string, port uint) *v1alpha1.DRBDResource {
		return &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "Internal", Address: v1alpha1.DRBDAddress{IPv4: ip, Port: port}},
				},
			},
		}
	}

	t.Run("no conflict - passes through nil", func(t *testing.T) {
		drbdr := makeDRBDR("10.0.0.1", 7000)
		result := ensureLocalPortConflictResolved(context.Background(), drbdr, nil, nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
		if drbdr.Status.Addresses[0].Address.Port != 7000 {
			t.Errorf("port changed unexpectedly: %d", drbdr.Status.Addresses[0].Address.Port)
		}
	})

	t.Run("no conflict - passes through unrelated error", func(t *testing.T) {
		drbdr := makeDRBDR("10.0.0.1", 7000)
		origErr := fmt.Errorf("some other error")
		result := ensureLocalPortConflictResolved(context.Background(), drbdr, origErr, nil)
		if result != origErr {
			t.Errorf("expected original error, got %v", result)
		}
		if drbdr.Status.Addresses[0].Address.Port != 7000 {
			t.Errorf("port changed unexpectedly: %d", drbdr.Status.Addresses[0].Address.Port)
		}
	})

	t.Run("conflict - re-allocates port", func(t *testing.T) {
		drbdr := makeDRBDR("10.0.0.1", 7000)
		drbdErr := makeConflictErr("10.0.0.1")
		allocator := func(_ context.Context, ip string) (uint, error) {
			if ip != "10.0.0.1" {
				t.Errorf("allocator called with unexpected IP: %s", ip)
			}
			return 7042, nil
		}

		result := ensureLocalPortConflictResolved(context.Background(), drbdr, drbdErr, allocator)

		if drbdr.Status.Addresses[0].Address.Port != 7042 {
			t.Errorf("port = %d, want 7042", drbdr.Status.Addresses[0].Address.Port)
		}
		if result == nil {
			t.Fatal("expected annotated error, got nil")
		}
		if !errors.Is(result, drbdutils.ErrNewPathLocalAddrInUse) {
			t.Errorf("error chain lost ErrNewPathLocalAddrInUse: %v", result)
		}
		errMsg := result.Error()
		if !contains(errMsg, "port re-allocated: 7000 -> 7042") {
			t.Errorf("error missing re-allocation annotation: %s", errMsg)
		}
	})

	t.Run("conflict - allocation fails, port unchanged", func(t *testing.T) {
		drbdr := makeDRBDR("10.0.0.1", 7000)
		drbdErr := makeConflictErr("10.0.0.1")
		allocator := func(_ context.Context, _ string) (uint, error) {
			return 0, fmt.Errorf("no free port")
		}

		result := ensureLocalPortConflictResolved(context.Background(), drbdr, drbdErr, allocator)

		if drbdr.Status.Addresses[0].Address.Port != 7000 {
			t.Errorf("port changed to %d, want 7000 (unchanged on alloc failure)", drbdr.Status.Addresses[0].Address.Port)
		}
		if result == nil {
			t.Fatal("expected error, got nil")
		}
		errMsg := result.Error()
		if !contains(errMsg, "port re-allocation failed") {
			t.Errorf("error missing failure annotation: %s", errMsg)
		}
	})

	t.Run("conflict - IP not in addresses, no change", func(t *testing.T) {
		drbdr := makeDRBDR("10.0.0.1", 7000)
		drbdErr := makeConflictErr("10.0.0.99")

		result := ensureLocalPortConflictResolved(context.Background(), drbdr, drbdErr, nil)

		if drbdr.Status.Addresses[0].Address.Port != 7000 {
			t.Errorf("port changed unexpectedly: %d", drbdr.Status.Addresses[0].Address.Port)
		}
		if !errors.Is(result, drbdutils.ErrNewPathLocalAddrInUse) {
			t.Errorf("expected original error passed through, got %v", result)
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
