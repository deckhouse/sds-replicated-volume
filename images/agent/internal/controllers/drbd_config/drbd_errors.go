package drbdconfig

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

const maxErrLen = 1024

type drbdAPIError interface {
	error
	ToDRBDErrors(apiErrors *v1alpha3.DRBDErrors)
}

func trimLen(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[0:maxLen]
	}
	return s
}

type configurationCommandError struct{ drbdadm.CommandError }

type fileSystemOperationError struct{ error }

var _ drbdAPIError = configurationCommandError{}
var _ drbdAPIError = fileSystemOperationError{}

func (c configurationCommandError) ToDRBDErrors(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.ConfigurationCommandError = &v1alpha3.CmdError{
		Command:  trimLen(strings.Join(c.CommandWithArgs(), " "), maxErrLen),
		Output:   trimLen(c.Output(), maxErrLen),
		ExitCode: c.ExitCode(),
	}
}

func (f fileSystemOperationError) ToDRBDErrors(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.FileSystemOperationError = &v1alpha3.MessageError{
		Message: trimLen(f.Error(), maxErrLen),
	}
}
