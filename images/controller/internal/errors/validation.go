package errors

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
