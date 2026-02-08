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

package flow

import (
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func newConflictError() error {
	return apierrors.NewConflict(schema.GroupResource{Resource: "test"}, "obj", errors.New("optimistic lock"))
}

func TestIsExpectedTransientError_Nil(t *testing.T) {
	if isExpectedTransientError(nil) {
		t.Fatal("expected false for nil")
	}
}

func TestIsExpectedTransientError_PlainError(t *testing.T) {
	if isExpectedTransientError(errors.New("something")) {
		t.Fatal("expected false for plain error")
	}
}

func TestIsExpectedTransientError_ConflictLeaf(t *testing.T) {
	if !isExpectedTransientError(newConflictError()) {
		t.Fatal("expected true for conflict error")
	}
}

func TestIsExpectedTransientError_WrappedConflict(t *testing.T) {
	err := Wrapf(newConflictError(), "patching RSP")
	if !isExpectedTransientError(err) {
		t.Fatal("expected true for Wrapf-wrapped conflict")
	}
}

func TestIsExpectedTransientError_DoubleWrappedConflict(t *testing.T) {
	err := Wrapf(Wrapf(newConflictError(), "inner"), "outer")
	if !isExpectedTransientError(err) {
		t.Fatal("expected true for double-wrapped conflict")
	}
}

func TestIsExpectedTransientError_FmtWrappedConflict(t *testing.T) {
	err := fmt.Errorf("context: %w", newConflictError())
	if !isExpectedTransientError(err) {
		t.Fatal("expected true for fmt.Errorf %%w wrapped conflict")
	}
}

func TestIsExpectedTransientError_JoinedAllConflicts(t *testing.T) {
	err := errors.Join(newConflictError(), newConflictError())
	if !isExpectedTransientError(err) {
		t.Fatal("expected true when all joined errors are conflicts")
	}
}

func TestIsExpectedTransientError_JoinedMixed(t *testing.T) {
	err := errors.Join(newConflictError(), errors.New("real error"))
	if isExpectedTransientError(err) {
		t.Fatal("expected false when joined errors contain a non-conflict")
	}
}

func TestIsExpectedTransientError_WrappedJoinedMixed(t *testing.T) {
	joined := errors.Join(newConflictError(), errors.New("real error"))
	err := fmt.Errorf("context: %w", joined)
	if isExpectedTransientError(err) {
		t.Fatal("expected false for wrapped joined error with non-conflict component")
	}
}

func TestIsExpectedTransientError_WrappedJoinedAllConflicts(t *testing.T) {
	joined := errors.Join(newConflictError(), newConflictError())
	err := fmt.Errorf("context: %w", joined)
	if !isExpectedTransientError(err) {
		t.Fatal("expected true for wrapped joined error where all components are conflicts")
	}
}

func TestIsExpectedTransientError_NotFoundIsNotTransient(t *testing.T) {
	err := apierrors.NewNotFound(schema.GroupResource{Resource: "test"}, "obj")
	if isExpectedTransientError(err) {
		t.Fatal("expected false for NotFound error")
	}
}

func TestIsExpectedTransientError_InternalServerIsNotTransient(t *testing.T) {
	err := apierrors.NewInternalError(errors.New("oops"))
	if isExpectedTransientError(err) {
		t.Fatal("expected false for InternalServerError")
	}
}
