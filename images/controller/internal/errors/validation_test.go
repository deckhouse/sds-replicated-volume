package errors_test

import (
	"testing"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func TestValidateArgNotNil(t *testing.T) {
	var err error

	err = errors.ValidateArgNotNil(nil, "testArgName")
	if err == nil {
		t.Fatal("ValidateArgNotNil() succeeded unexpectedly")
	}

	timeArg := time.Now()
	timeArgPtr := &timeArg

	err = errors.ValidateArgNotNil(timeArgPtr, "timeArgPtr")
	if err != nil {
		t.Fatalf("ValidateArgNotNil() failed: %v", err)
	}

	timeArgPtr = nil

	err = errors.ValidateArgNotNil(timeArgPtr, "testArgName")
	if err == nil {
		t.Fatal("ValidateArgNotNil() succeeded unexpectedly")
	}
}
