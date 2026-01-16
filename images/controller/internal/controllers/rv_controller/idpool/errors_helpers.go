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

package idpool

import "errors"

// IsDuplicateID reports whether err is (or wraps) a DuplicateIDError.
// Similar to apierrors.IsNotFound, it supports wrapped errors via errors.As.
func IsDuplicateID(err error) bool {
	_, ok := AsDuplicateID(err)
	return ok
}

// IsPoolExhausted reports whether err is (or wraps) a PoolExhaustedError.
func IsPoolExhausted(err error) bool {
	_, ok := AsPoolExhausted(err)
	return ok
}

// IsNameConflict reports whether err is (or wraps) a NameConflictError.
func IsNameConflict(err error) bool {
	_, ok := AsNameConflict(err)
	return ok
}

// IsOutOfRange reports whether err is (or wraps) an OutOfRangeError.
func IsOutOfRange(err error) bool {
	_, ok := AsOutOfRange(err)
	return ok
}

// AsDuplicateID extracts a DuplicateIDError from err (including wrapped errors).
func AsDuplicateID(err error) (DuplicateIDError, bool) {
	var e DuplicateIDError
	if errors.As(err, &e) {
		return e, true
	}
	return DuplicateIDError{}, false
}

// AsPoolExhausted extracts a PoolExhaustedError from err (including wrapped errors).
func AsPoolExhausted(err error) (PoolExhaustedError, bool) {
	var e PoolExhaustedError
	if errors.As(err, &e) {
		return e, true
	}
	return PoolExhaustedError{}, false
}

// AsNameConflict extracts a NameConflictError from err (including wrapped errors).
func AsNameConflict(err error) (NameConflictError, bool) {
	var e NameConflictError
	if errors.As(err, &e) {
		return e, true
	}
	return NameConflictError{}, false
}

// AsOutOfRange extracts an OutOfRangeError from err (including wrapped errors).
func AsOutOfRange(err error) (OutOfRangeError, bool) {
	var e OutOfRangeError
	if errors.As(err, &e) {
		return e, true
	}
	return OutOfRangeError{}, false
}
