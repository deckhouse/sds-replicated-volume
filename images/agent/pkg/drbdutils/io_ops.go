package drbdutils

import (
	"context"
	"strconv"
)

var SuspendIOArgs = func(minor uint) []string {
	return []string{
		"suspend-io",
		strconv.FormatUint(uint64(minor), 10),
	}
}

func ExecuteSuspendIO(ctx context.Context, minor uint) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, SuspendIOArgs(minor)...)
	_, err := executeCommand(cmd, nil)
	return err
}

var ResumeIOArgs = func(minor uint) []string {
	return []string{
		"resume-io",
		strconv.FormatUint(uint64(minor), 10),
	}
}

func ExecuteResumeIO(ctx context.Context, minor uint) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, ResumeIOArgs(minor)...)
	_, err := executeCommand(cmd, nil)
	return err
}
