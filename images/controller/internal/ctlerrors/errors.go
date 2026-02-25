/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ctlerrors

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
