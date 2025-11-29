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

package drbdconf

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

/*
# Mapping of Parameter Types

All primitive types' zero values should semantically correspond to a missing
DRBD section parameter (even for required parameters).

# Tags

  - `drbd:"parametername"` to select the name of the parameter. There can be one
    parameterless tag: `drbd:""`, which selects key of the section byitself
  - [SectionKeyworder] and slices of such types SHOULD NOT be tagged, their name
    is always taken from [SectionKeyworder]
  - subsections should always be represented with struct pointers
  - `drbd:"parname1,parname2"` tag value form allows specifying alternative
    parameter names, which will be tried during unmarshaling. Marshaling will
    always use the first name.

# Primitive Types Support

To add marshaling/unmarshaling support for another primitive type, consider the
following options:
  - implement [ParameterTypeCodec] and register it with
    [RegisterParameterTypeCodec]. It will be used for every usage of that type,
    with highest priority. It will even take precendence over built-in slice
    support. This method is useful for fields of "marker" interface types.
  - implement [ParameterCodec]. This marshaling method is last-effort method,
    it is used when there's no [ParameterTypeCodec] for a type
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

				pars, err := marshalParameters(f.Field, f.FieldVal)
				if err != nil {
					return err
				}

				if f.ParameterNames[0] == "" {
					if len(pars) > 1 {
						return fmt.Errorf(
							"marshaling field %s: can not "+
								"render more then one parameter value to key",
							f.Field.Name,
						)
					}
					// current section key
					dst.Key = append(dst.Key, pars[0]...)
				} else {
					for _, words := range pars {
						// new parameter
						par := &Parameter{}
						par.Key = append(par.Key, NewWord(f.ParameterNames[0]))
						par.Key = append(par.Key, words...)
						dst.Elements = append(dst.Elements, par)
					}
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

func marshalParameters(
	field reflect.StructField,
	fieldVal reflect.Value,
) ([][]Word, error) {
	parsStrs, err := marshalParameterValue(fieldVal, field.Type)
	if err != nil {
		return nil, fmt.Errorf("marshaling field %s: %w", field.Name, err)
	}

	var pars [][]Word
	for _, parStr := range parsStrs {
		pars = append(pars, NewWords(parStr))
	}

	return pars, nil
}

func marshalParameterValue(
	srcVal reflect.Value,
	srcType reflect.Type,
) ([][]string, error) {
	if typeCodec := parameterTypeCodecs[srcType]; typeCodec != nil {
		res, err := typeCodec.MarshalParameter(srcVal.Interface())
		if err != nil {
			return nil, err
		}
		return [][]string{res}, nil
	}

	// value type may be different in case when srcType is slice element type
	if srcVal.Type() != srcType {
		if typeCodec := parameterTypeCodecs[srcVal.Type()]; typeCodec != nil {
			resItem, err := typeCodec.MarshalParameter(srcVal.Interface())
			if err != nil {
				return nil, err
			}
			return [][]string{resItem}, nil
		}
	}

	if srcType.Kind() == reflect.Slice {
		var res [][]string
		for i := 0; i < srcVal.Len(); i++ {
			elVal := srcVal.Index(i)

			elRes, err := marshalParameterValue(elVal, srcType.Elem())
			if err != nil {
				return nil, err
			}
			if len(elRes) > 1 {
				return nil, errors.New(
					"marshaling slices of slices is not supported",
				)
			}
			res = append(res, elRes[0])
		}
		return res, nil
	}

	if m, ok := srcVal.Interface().(ParameterMarshaler); ok {
		resItem, err := m.MarshalParameter()
		if err != nil {
			return nil, err
		}
		return [][]string{resItem}, nil
	}

	// interface may be implemented for pointer receiver
	if srcVal.Kind() != reflect.Pointer {
		if m, ok := srcVal.Addr().Interface().(ParameterMarshaler); ok {
			resItem, err := m.MarshalParameter()
			if err != nil {
				return nil, err
			}
			return [][]string{resItem}, nil
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
