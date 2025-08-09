package drbdadm

import (
	"context"
	"os/exec"
)

func ExecutePrimary(ctx context.Context, resource string) error {
	args := PrimaryArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)
	return cmd.Run()
}

func ExecuteSecondary(ctx context.Context, resource string) error {
	args := SecondaryArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)
	return cmd.Run()
}
