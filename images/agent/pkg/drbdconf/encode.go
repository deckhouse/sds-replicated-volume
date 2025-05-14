package drbdconf

import (
	"fmt"
	"reflect"
)

/*
# Mapping of Parameter Types

All primitive types' zero values should semantically correspond to a missing
DRBD section parameter (even for required parameters).

Supported primitive types:
  - [string]
  - [bool]
  - [*int]
  - slices of [string]
  - Custom types, which implement [ParameterCodec]
  - TODO (IPs, sectors, bytes, etc.).

# Tags

  - `drbd:"parametername"` to select the name of the parameter. There can be one
    parameterless tag: `drbd:""`, which selects key of the section byitself
  - [SectionKeyworder] and slices of such types SHOULD NOT be tagged, their name
    is always taken from [SectionKeyworder]
*/
func Marshal[T any, TP Ptr[T]](v TP) ([]*Section, error) {
	if v == nil {
		return nil, fmt.Errorf("expected non-nil pointer to a struct")
	}

	val := reflect.ValueOf(v)

	if val.Kind() != reflect.Pointer || val.IsNil() {
		return nil, fmt.Errorf("expected non-nil pointer to a struct")
	}

	val = val.Elem()

	valType := val.Type()
	for i := 0; i < valType.NumField(); i++ {
		field := valType.Field(i)

		// skip unexported fields
		if field.PkgPath != "" {
			continue
		}

		fieldVal := val.Field(i)

		// Check for drbd tag indicating a parameter field.
		tagValue, tagValueFound := field.Tag.Lookup("drbd")

		if tagValueFound {

			if fieldVal.IsZero() {
				// zero values always mean a missing parameter
				continue
			}

			// current section key
			// TODO

			var codec ParameterCodec
			codec = BuiltinParameterCodecs[field.Type]

			// fieldVal.

			if codec == nil {
				if c, ok := fieldVal.Interface().(ParameterCodec); ok {
					codec = c
				}
			}

			if codec == nil {
				return nil, fmt.Errorf(
					"field tagged, but ParameterCodec for type %s is not found",
					field.Type,
				)
			}

			codec.MarshalParameter()
		} else if sec, ok := fieldVal.Interface().(SectionKeyworder); ok {
			// subsection
			// TODO

		} else {
			// skip field
			continue
		}
	}
	return nil, nil
}

func Unmarshal[T any, PT Ptr[T]](sections []*Section, v PT) error {
	return nil
}
