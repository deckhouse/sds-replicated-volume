package drbdconfig

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

type drbdAPIError interface {
	error
	WriteDRBDError(apiErrors *v1alpha3.DRBDErrors)
	// should be callable with zero receiver
	ResetDRBDError(apiErrors *v1alpha3.DRBDErrors)
}

// all errors

type configurationCommandError struct{ drbdadm.CommandError }

type fileSystemOperationError struct{ error }

type sharedSecretAlgUnsupportedError struct {
	error
	unsupportedAlg string
}

// [drbdAPIError]

var allDRBDAPIErrors []drbdAPIError = []drbdAPIError{
	configurationCommandError{},
	fileSystemOperationError{},
	sharedSecretAlgUnsupportedError{},
}

func resetAllDRBDAPIErrors(apiErrors *v1alpha3.DRBDErrors) {
	for _, e := range allDRBDAPIErrors {
		e.ResetDRBDError(apiErrors)
	}
}

// [drbdAPIError.WriteDRBDError]

func (c configurationCommandError) WriteDRBDError(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.ConfigurationCommandError = &v1alpha3.CmdError{
		Command:  trimLen(strings.Join(c.CommandWithArgs(), " "), maxErrLen),
		Output:   trimLen(c.Output(), maxErrLen),
		ExitCode: c.ExitCode(),
	}
}

func (f fileSystemOperationError) WriteDRBDError(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.FileSystemOperationError = &v1alpha3.MessageError{
		Message: trimLen(f.Error(), maxErrLen),
	}
}

func (s sharedSecretAlgUnsupportedError) WriteDRBDError(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.SharedSecretAlgSelectionError = &v1alpha3.SharedSecretUnsupportedAlgError{
		UnsupportedAlg: s.unsupportedAlg,
	}
}

// [drbdAPIError.ResetDRBDError]

func (configurationCommandError) ResetDRBDError(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.ConfigurationCommandError = nil
}

func (fileSystemOperationError) ResetDRBDError(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.FileSystemOperationError = nil
}

func (sharedSecretAlgUnsupportedError) ResetDRBDError(apiErrors *v1alpha3.DRBDErrors) {
	apiErrors.SharedSecretAlgSelectionError = nil
}

// utils

const maxErrLen = 1024

func trimLen(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[0:maxLen]
	}
	return s
}
