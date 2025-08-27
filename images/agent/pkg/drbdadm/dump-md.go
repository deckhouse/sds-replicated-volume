package drbdadm

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ExecuteDumpMD executes a command and returns:
// - (true, nil) if it exits with code 0
// - (false, nil) if it exits with code 1 and contains "No valid meta data found"
// - (false, error) for any other case
func ExecuteDumpMD_MetadataExists(ctx context.Context, resource string) (bool, error) {
	cmd := exec.CommandContext(ctx, Command, DumpMDArgs(resource)...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		output := stderr.String()

		if exitCode == 1 && strings.Contains(output, "No valid meta data found") {
			return false, nil
		}
	}

	return false, errors.Join(err, errors.New(stderr.String()))
}
