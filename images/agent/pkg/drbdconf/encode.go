package drbdconf

import (
	"fmt"
	"reflect"
	"strings"
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
  - `drbd:"parname1,parname2"` tag value form allows specifying alternative
    parameter names, which will be tried during unmarshaling. Marshaling will
    always use the first name.
*/
func Marshal[T any, TP Ptr[T]](src TP, dst *Section) error {
	return marshalSection(reflect.ValueOf(src), dst)
}

func marshalSection(srcPtrVal reflect.Value, dst *Section) error {
	err := visitStructFields(
		srcPtrVal,
		func(f *visitedField) error {
			if len(f.ParameterNames) > 0 {
				// zero values always mean a missing parameter
				if isZeroValue(f.FieldVal) {
					return nil
				}

				words, err := marshalParameter(f.Field, f.FieldVal)
				if err != nil {
					return err
				}

				if f.ParameterNames[0] == "" {
					// current section key
					dst.Key = append(dst.Key, words...)
				} else {
					// new parameter
					par := &Parameter{}
					par.Key = append(par.Key, NewWord(f.ParameterNames[0]))
					par.Key = append(par.Key, words...)
					dst.Elements = append(dst.Elements, par)
				}
			} else if ok, _, kw := isSliceOfStructPtrsAndSectionKeyworders(
				f.Field.Type,
			); ok {
				for i := range f.FieldVal.Len() {
					elem := f.FieldVal.Index(i)

					subsecItem := &Section{Key: []Word{NewWord(kw)}}
					err := marshalSection(elem, subsecItem)
					if err != nil {
						return fmt.Errorf(
							"marshaling field %s, item %d: %w",
							f.Field.Name, i, err,
						)
					}
					dst.Elements = append(dst.Elements, subsecItem)
				}
			} else if ok, kw := isStructPtrAndSectionKeyworder(f.FieldVal); ok {
				subsec := &Section{Key: []Word{NewWord(kw)}}
				err := marshalSection(f.FieldVal, subsec)
				if err != nil {
					return fmt.Errorf(
						"marshaling field %s: %w",
						f.Field.Name, err,
					)
				}
				dst.Elements = append(dst.Elements, subsec)
			}
			return nil
		},
	)

	if err != nil {
		return err
	}

	return nil
}

func isStructPtrAndSectionKeyworder(v reflect.Value) (ok bool, kw string) {
	ok = isNonNilStructPtr(v) &&
		v.Type().Implements(reflect.TypeFor[SectionKeyworder]())
	if ok {
		kw = v.Interface().(SectionKeyworder).SectionKeyword()
	}
	return
}

// TODO
// func isSliceOfStructPtrsAndSectionKeyworders(v reflect.Value) bool {
// 	ok = isNonNilStructPtr(v) &&
// 		v.Type().Implements(reflect.TypeFor[SectionKeyworder]())
// 	if ok {
// 		kw = v.Interface().(SectionKeyworder).SectionKeyword()
// 	}
// 	return
// }

func marshalParameter(
	field reflect.StructField,
	fieldVal reflect.Value,
) ([]Word, error) {
	if field.Type.Kind() == reflect.Slice {
		wordStrs := make([]string, fieldVal.Len())
		for i := range fieldVal.Len() {
			itemWordStrs, err := marshalParameterValue(
				fieldVal.Index(i),
				field.Type.Elem(),
			)
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

	wordStrs, err := marshalParameterValue(fieldVal, field.Type)
	if err != nil {
		return nil, fmt.Errorf("marshaling field %s: %w", field.Name, err)
	}

	return NewWords(wordStrs), nil
}

func marshalParameterValue(
	srcVal reflect.Value,
	srcType reflect.Type,
) ([]string, error) {
	if typeCodec := ParameterTypeCodecs[srcType]; typeCodec != nil {
		return typeCodec.MarshalParameter(srcVal.Interface())
	}

	// value type may be different in case when srcType is slice element type
	if srcVal.Type() != srcType {
		if typeCodec := ParameterTypeCodecs[srcVal.Type()]; typeCodec != nil {
			return typeCodec.MarshalParameter(srcVal.Interface())
		}
	}

	if m, ok := srcVal.Interface().(ParameterMarshaler); ok {
		return m.MarshalParameter()
	}

	// interface may be implemented for pointer receiver
	if srcVal.Kind() != reflect.Pointer {
		if m, ok := srcVal.Addr().Interface().(ParameterMarshaler); ok {
			return m.MarshalParameter()
		}
	}

	return nil, fmt.Errorf("unsupported field type")
}

func isZeroValue(v reflect.Value) bool {
	if v.IsZero() {
		return true
	}
	if v.Kind() == reflect.Slice && v.Len() == 0 {
		return true
	}
	return false
}

func getDRBDParameterNames(field reflect.StructField) ([]string, error) {
	tagValue, ok := field.Tag.Lookup("drbd")
	if !ok {
		return nil, nil
	}

	tagValue = strings.TrimSpace(tagValue)

	if tagValue == "" {
		return []string{""}, nil
	}

	names := strings.Split(tagValue, ",")
	for i, n := range names {
		n = strings.TrimSpace(n)
		if len(n) == 0 || !isTokenStr(n) {
			return nil,
				fmt.Errorf(
					"field %s tag `drbd` value: invalid format",
					field.Name,
				)
		}
		names[i] = n
	}
	return names, nil
}

func isNonNilStructPtr(v reflect.Value) bool {
	return v.Kind() == reflect.Pointer &&
		!v.IsNil() &&
		v.Elem().Kind() == reflect.Struct
}

func isSectionKeyworder(v reflect.Value) bool {
	return v.Type().Implements(reflect.TypeFor[SectionKeyworder]())
}
