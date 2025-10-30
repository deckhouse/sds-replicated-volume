package drbdadm

import (
	"context"
	"errors"
	"os/exec"
)

func ExecuteCreateMD(ctx context.Context, resource string) error {
	cmd := exec.CommandContext(ctx, Command, CreateMDArgs(resource)...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Join(err, errors.New(string(out)))
	}

	return nil
}
