package drbdadm

type CommandError interface {
	error
	CommandWithArgs() []string
	Output() string
	ExitCode() int
}

var _ CommandError = &commandError{}

type commandError struct {
	error
	commandWithArgs []string
	output          string
	exitCode        int
}

func (e *commandError) CommandWithArgs() []string {
	return e.commandWithArgs
}

func (e *commandError) Error() string {
	return e.error.Error()
}

func (e *commandError) ExitCode() int {
	return e.exitCode
}

func (e *commandError) Output() string {
	return e.output
}
