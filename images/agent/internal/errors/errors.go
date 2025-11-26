package errors

import (
	"errors"
	"fmt"
)

var ErrNotImplemented = errors.New("not implemented")

var ErrInvalidCluster = errors.New("invalid cluster state")

var ErrInvalidNode = errors.New("invalid node")

var ErrUnknown = errors.New("unknown error")

func WrapErrorf(err error, format string, a ...any) error {
	return fmt.Errorf("%w: %w", err, fmt.Errorf(format, a...))
}

func ErrInvalidClusterf(format string, a ...any) error {
	return WrapErrorf(ErrInvalidCluster, format, a...)
}

func ErrInvalidNodef(format string, a ...any) error {
	return WrapErrorf(ErrInvalidNode, format, a...)
}

func ErrNotImplementedf(format string, a ...any) error {
	return WrapErrorf(ErrNotImplemented, format, a...)
}

func ErrUnknownf(format string, a ...any) error {
	return WrapErrorf(ErrUnknown, format, a...)
}
