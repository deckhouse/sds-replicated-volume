package drbdconfig

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type drbdOperationError interface {
	error
	ToDRBDErrors(*v1alpha3.DRBDErrors)
}

func trimLen(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[0:maxLen]
	}
	return s
}

type configValidationError struct {
	error
	output   string
	exitCode int
}

var _ drbdOperationError = configValidationError{}

func (c configValidationError) ToDRBDErrors(e *v1alpha3.DRBDErrors) {
	e.ConfigValidationError = &v1alpha3.CmdError{
		Output:   trimLen(c.output, 1024),
		ExitCode: c.exitCode,
	}
}
