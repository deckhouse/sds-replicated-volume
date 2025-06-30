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
// - (false, nil) if it exits with code 10 and contains "No such resource"
// - (false, error) for any other case
func ExecuteStatus_IsUp(ctx context.Context, resource string) (bool, error) {
	cmd := exec.CommandContext(ctx, Command, StatusArgs(resource)...)

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

		if exitCode == 10 && strings.Contains(output, "No such resource") {
			return false, nil
		}
	}

	return false, errors.Join(err, errors.New(stderr.String()))
}
