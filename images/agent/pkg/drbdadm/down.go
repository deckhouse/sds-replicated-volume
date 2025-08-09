package drbdadm

import (
	"context"
	"os/exec"
)

func ExecuteDown(ctx context.Context, resource string) error {
	args := DownArgs(resource)
	cmd := exec.CommandContext(ctx, Command, args...)
	return cmd.Run()
}
