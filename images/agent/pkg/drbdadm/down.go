package drbdadm

import (
	"context"
	"errors"
	"os/exec"
)

func ExecuteDown(ctx context.Context, resource string) error {
	args := DownArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Join(err, errors.New(string(out)))
	}

	return nil
}
