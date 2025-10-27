package cluster2

import (
	"errors"
	"fmt"
)

var MaxNodeId = uint(7)

func errArg(format string, a ...any) error {
	return fmt.Errorf("invalid argument: %w", fmt.Errorf(format, a...))
}

func errArgNil(argName string) error {
	return fmt.Errorf("invalid argument: expected %s not to be nil", argName)
}

func errUnexpected(why string) error {
	return fmt.Errorf("unexpected error: %s", why)
}

var ErrInvalidCluster = errors.New("invalid cluster state")
var ErrInvalidNode = errors.New("invalid node")

func errInvalidCluster(format string, a ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidCluster, fmt.Errorf(format, a...))
}

func errInvalidNode(format string, a ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidNode, fmt.Errorf(format, a...))
}
