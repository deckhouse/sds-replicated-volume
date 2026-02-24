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
	"fmt"
	"reflect"
)

func ValidateArgNotNil(arg any, argName string) error {
	if arg == nil {
		return fmt.Errorf("expected '%s' to be non-nil", argName)
	}
	// Check for typed nil pointers (e.g., (*SomeStruct)(nil) passed as any)
	v := reflect.ValueOf(arg)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return fmt.Errorf("expected '%s' to be non-nil", argName)
	}
	return nil
}
