package drbdadm

import (
	"context"
	"errors"
	"os/exec"
)

func ExecutePrimary(ctx context.Context, resource string) error {
	args := PrimaryArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Join(err, errors.New(string(out)))
	}

	return nil
}

func ExecutePrimaryForce(ctx context.Context, resource string) error {
	args := PrimaryForceArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Join(err, errors.New(string(out)))
	}

	return nil
}

func ExecuteSecondary(ctx context.Context, resource string) error {
	args := SecondaryArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Join(err, errors.New(string(out)))
	}

	return nil
}
