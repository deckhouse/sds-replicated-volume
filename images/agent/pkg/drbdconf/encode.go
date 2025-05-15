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
  - subsections should always be represented with struct pointers
*/
func Marshal[T any, TP Ptr[T]](v TP) ([]*Section, error) {
	s, err := marshalSection(reflect.ValueOf(v), true)
	if err != nil {
		return nil, err
	}

	sections := make([]*Section, 0, len(s.Elements))
	for _, el := range s.Elements {
		if sec, ok := el.(*Section); ok {
			sections = append(sections, sec)
		}
	}

	return sections, nil
}

func marshalSection(ptrVal reflect.Value, root bool) (*Section, error) {
	if !isNonNilStructPtr(ptrVal) {
		return nil, fmt.Errorf("expected non-nil pointer to a struct")
	}

	val := ptrVal.Elem()

	valType := val.Type()

	sec := &Section{}
	if !root {
		sec.Key = append(
			sec.Key,
			NewWord(ptrVal.Interface().(SectionKeyworder).SectionKeyword()),
		)
	}

	for i := range valType.NumField() {
		field := valType.Field(i)

		// skip unexported fields
		if field.PkgPath != "" {
			continue
		}

		fieldVal := val.Field(i)

		tagValue, tagValueFound := field.Tag.Lookup("drbd")

		if tagValueFound {
			if root {
				return nil,
					fmt.Errorf(
						"expected root section not to have parameters, but "+
							"`drbd` tag found on field %s",
						field.Name,
					)
			}

			if fieldVal.IsZero() {
				// zero values always mean a missing parameter
				continue
			}

			words, err := marshalParameter(field, fieldVal)
			if err != nil {
				return nil,
					fmt.Errorf(
						"marshaling struct %s: %w",
						valType.Name(), err,
					)
			}

			if tagValue == "" {
				// current section key
				sec.Key = append(sec.Key, words...)
			} else {
				// new parameter
				par := &Parameter{}
				par.Key = append(par.Key, NewWord(tagValue))
				par.Key = append(par.Key, words...)
				sec.Elements = append(sec.Elements, par)
			}
		} else if isStructPtrImplementingSectionKeyworder(fieldVal) {
			subsec, err := marshalSection(fieldVal, false)
			if err != nil {
				return nil,
					fmt.Errorf("marshaling field %s: %w", field.Name, err)
			}
			sec.Elements = append(sec.Elements, subsec)
		} else {
			// skip field
			continue
		}
	}

	return sec, nil
}

func isNonNilStructPtr(v reflect.Value) bool {
	return v.Kind() == reflect.Pointer &&
		!v.IsNil() &&
		v.Elem().Kind() == reflect.Struct
}

func isStructPtrImplementingSectionKeyworder(v reflect.Value) bool {
	return isNonNilStructPtr(v) &&
		v.Type().Implements(reflect.TypeFor[SectionKeyworder]())
}

func marshalParameter(
	field reflect.StructField,
	fieldVal reflect.Value,
) ([]Word, error) {

	if field.Type.Kind() == reflect.Slice {
		wordStrs := make([]string, fieldVal.Len())
		for i := range fieldVal.Len() {
			item := fieldVal.Index(i).Interface()

			itemWordStrs, err := marshalParameterValue(item, field.Type.Elem())
			if err != nil {
				return nil,
					fmt.Errorf(
						"marshaling field %s item %d: %w",
						field.Name, i, err,
					)
			}

			if len(itemWordStrs) != 1 {
				return nil,
					fmt.Errorf(
						"marshaling field %s item %d: "+
							"marshaler is expected to produce exactly "+
							"one word per item, got %d",
						field.Name, i, len(itemWordStrs),
					)
			}
			wordStrs[i] = itemWordStrs[0]
		}
		return NewWords(wordStrs), nil
	}

	wordStrs, err := marshalParameterValue(fieldVal.Interface(), field.Type)
	if err != nil {
		return nil, fmt.Errorf("marshaling field %s: %w", field.Name, err)
	}

	return NewWords(wordStrs), nil
}

func marshalParameterValue(v any, vtype reflect.Type) ([]string, error) {
	if typeCodec := ParameterTypeCodecs[vtype]; typeCodec != nil {
		return typeCodec.MarshalParameter(v)
	}

	if codec, ok := v.(ParameterCodec); ok {
		return codec.MarshalParameter()
	}

	return nil, fmt.Errorf("unsupported field type '%s'", vtype.Name())

}

func Unmarshal[T any, PT Ptr[T]](sections []*Section, v PT) error {
	return nil
}
