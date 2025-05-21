package drbdconf

import (
	"fmt"
	"reflect"
	"slices"
)

func Unmarshal[T any, PT Ptr[T]](src *Section, dst PT) error {
	err := unmarshalSection(src, reflect.ValueOf(dst))
	if err != nil {
		return err
	}

	return nil
}

func unmarshalSection(
	src *Section,
	ptrVal reflect.Value,
) error {
	err := visitStructFields(
		ptrVal,
		func(f *visitedField) error {
			if len(f.ParameterNames) > 0 {
				var par []Word
				if f.ParameterNames[0] == "" {
					// value is in current section key
					par = src.Key
				} else {
					// value is in parameters
					for _, parName := range f.ParameterNames {
						pars := slices.Collect(src.ParametersByKey(parName))

						if len(pars) > 1 {
							return fmt.Errorf(
								"unable to unmarshal duplicate parameter '%s' "+
									"into a field '%s'",
								parName, f.Field.Name,
							)
						} else if len(pars) == 1 {
							par = pars[0].Key
							// ignore the rest of ParameterNames
							break
						}
					}
				}

				if len(par) > 0 {
					return unmarshalParameterValue(
						par,
						f.FieldVal,
						f.Field.Type,
					)
				}
			} else if ok, elType, kw := isSliceOfStructPtrsAndSectionKeyworders(
				f.Field.Type,
			); ok {
				sliceIsNonEmpty := f.FieldVal.Len() > 0
				for subSection := range src.SectionsByKey(kw) {
					if sliceIsNonEmpty {
						return fmt.Errorf(
							"unmarshaling field %s: non-empty slice",
							f.Field.Name,
						)
					}
					newVal := reflect.New(elType.Elem())
					if err := unmarshalSection(subSection, newVal); err != nil {
						return fmt.Errorf(
							"unmarshaling section %s to field %s: %w",
							subSection.Location(),
							f.Field.Name,
							err,
						)
					}
					f.FieldVal.Set(reflect.Append(f.FieldVal, newVal))
				}
			} else if ok, kw := typeIsStructPtrAndSectionKeyworder(
				f.Field.Type,
			); ok {
				subSections := slices.Collect(src.SectionsByKey(kw))
				if len(subSections) == 0 {
					return nil
				}
				if len(subSections) > 1 {
					return fmt.Errorf(
						"unmarshaling field %s: "+
							"can not map more then one section",
						f.Field.Name,
					)
				}

				if f.FieldVal.IsNil() {
					newVal := reflect.New(f.FieldVal.Type().Elem())
					f.FieldVal.Set(newVal)
				}
				err := unmarshalSection(subSections[0], f.FieldVal)
				if err != nil {
					return fmt.Errorf(
						"unmarshaling section %s to field %s: %w",
						subSections[0].Location(),
						f.Field.Name,
						err,
					)
				}
			}
			return nil
		},
	)

	if err != nil {
		return err
	}

	return nil
}

type visitedField struct {
	Field          reflect.StructField
	FieldVal       reflect.Value
	ParameterNames []string
	SectionName    string
}

func visitStructFields(
	ptrVal reflect.Value,
	visit func(f *visitedField) error,
) error {
	if !isNonNilStructPtr(ptrVal) {
		return fmt.Errorf("expected non-nil pointer to a struct")
	}

	val := ptrVal.Elem()

	valType := val.Type()
	for i := range valType.NumField() {
		field := valType.Field(i)
		// skip unexported fields
		if field.PkgPath != "" {
			continue
		}

		fieldVal := val.Field(i)

		parNames, err := getDRBDParameterNames(field)
		if err != nil {
			return err
		}

		if !isSectionKeyworder(ptrVal) && len(parNames) > 0 {
			return fmt.Errorf(
				"`drbd` tag found on non-section type %s",
				valType.Name(),
			)
		}

		_, secName := isStructPtrAndSectionKeyworder(fieldVal)

		err = visit(
			&visitedField{
				Field:          field,
				FieldVal:       fieldVal,
				ParameterNames: parNames,
				SectionName:    secName,
			},
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func unmarshalParameterValue(
	srcPar []Word,
	dstVal reflect.Value,
	dstType reflect.Type,
) error {
	if typeCodec := ParameterTypeCodecs[dstType]; typeCodec != nil {
		v, err := typeCodec.UnmarshalParameter(srcPar)
		if err != nil {
			return err
		}

		val := reflect.ValueOf(v)

		if !val.Type().AssignableTo(dstVal.Type()) {
			return fmt.Errorf(
				"type codec returned value of type %s, which is not "+
					"assignable to destination type %s",
				val.Type().Name(), dstVal.Type().Name(),
			)
		}

		dstVal.Set(val)
		return nil
	}

	// value type may be different in case when dstType is slice element type
	if dstVal.Type() != dstType {
		if typeCodec := ParameterTypeCodecs[dstVal.Type()]; typeCodec != nil {
			v, err := typeCodec.UnmarshalParameter(srcPar)
			if err != nil {
				return err
			}
			val := reflect.ValueOf(v)

			if !val.Type().AssignableTo(dstVal.Type()) {
				return fmt.Errorf(
					"type codec returned value of type %s, which is not "+
						"assignable to destination type %s",
					val.Type().Name(), dstVal.Type().Name(),
				)
			}

			dstVal.Set(val)
			return nil
		}
	}

	if dstVal.Kind() == reflect.Pointer {
		if dstVal.Type().Implements(reflect.TypeFor[ParameterUnmarshaler]()) {
			if dstVal.IsNil() {
				dstVal.Set(reflect.New(dstVal.Type().Elem()))
			}
			return dstVal.
				Interface().(ParameterUnmarshaler).
				UnmarshalParameter(srcPar)
		}
	} else if um, ok := dstVal.Addr().Interface().(ParameterUnmarshaler); ok {
		return um.UnmarshalParameter(srcPar)
	}

	println("here")
	return fmt.Errorf("unsupported field type")
}

func isSliceOfStructPtrsAndSectionKeyworders(
	t reflect.Type,
) (ok bool, elType reflect.Type, kw string) {
	if t.Kind() != reflect.Slice {
		return
	}
	elType = t.Elem()
	ok, kw = typeIsStructPtrAndSectionKeyworder(elType)
	return
}

func typeIsStructPtrAndSectionKeyworder(t reflect.Type) (ok bool, kw string) {
	ok = t.Kind() == reflect.Pointer &&
		t.Elem().Kind() == reflect.Struct &&
		t.Implements(reflect.TypeFor[SectionKeyworder]())
	if ok {
		kw = reflect.Zero(t).
			Interface().(SectionKeyworder).
			SectionKeyword()
	}
	return
}
