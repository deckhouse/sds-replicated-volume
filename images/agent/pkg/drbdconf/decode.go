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
				var selectedSrcPars [][]Word
				if f.ParameterNames[0] == "" {
					// value is in current section key
					if len(src.Key) > 1 {
						selectedSrcPars = append(selectedSrcPars, src.Key)
					}
				} else {
					// value is in parameters
					for _, parName := range f.ParameterNames {
						srcPars := slices.Collect(src.ParametersByKey(parName))

						for _, srcPar := range srcPars {
							selectedSrcPars = append(
								selectedSrcPars,
								srcPar.Key,
							)
						}

						if len(srcPars) > 0 {
							// ignore the rest of ParameterNames
							break
						}
					}
				}

				if len(selectedSrcPars) > 0 {
					return unmarshalParameterValue(
						selectedSrcPars,
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
							"can not map more then one section: "+
							"%s, %s",
						f.Field.Name,
						subSections[0].Location(),
						subSections[1].Location(),
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

func unmarshalParameterValue(
	srcPars [][]Word,
	dstVal reflect.Value,
	dstType reflect.Type,
) error {
	// parameterTypeCodecs have the highest priority
	if typeCodec := parameterTypeCodecs[dstType]; typeCodec != nil {
		if len(srcPars) > 1 {
			return fmt.Errorf("can not map more then one section")
		}

		v, err := typeCodec.UnmarshalParameter(srcPars[0])
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
		if typeCodec := parameterTypeCodecs[dstVal.Type()]; typeCodec != nil {
			if len(srcPars) > 1 {
				return fmt.Errorf("can not map more then one section")
			}

			v, err := typeCodec.UnmarshalParameter(srcPars[0])
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

	if dstVal.Kind() == reflect.Slice {
		elType := dstType.Elem()
		elVarType := elType
		if elType.Kind() == reflect.Pointer {
			elVarType = elType.Elem()
		}
		for i, srcPar := range srcPars {
			elVar := reflect.New(elVarType)
			err := unmarshalParameterValue(
				[][]Word{srcPar},
				elVar,
				reflect.PointerTo(elVarType),
			)
			if err != nil {
				return fmt.Errorf(
					"unmarshaling parameter at %s to slice element %d "+
						"of type %s: %w",
					srcPar[len(srcPar)-1].Location, i,
					elType.Name(), err,
				)
			}
			if elType.Kind() != reflect.Pointer {
				elVar = elVar.Elem()
			}
			dstVal.Set(reflect.Append(dstVal, elVar))
		}
		return nil
	}

	if len(srcPars) > 1 {
		return fmt.Errorf("can not map more then one section")
	}

	if dstVal.Kind() == reflect.Pointer {
		if dstVal.Type().Implements(reflect.TypeFor[ParameterUnmarshaler]()) {
			if dstVal.IsNil() {
				newVal := reflect.New(dstVal.Type().Elem())
				dstVal.Set(newVal)
			}
			return dstVal.
				Interface().(ParameterUnmarshaler).
				UnmarshalParameter(srcPars[0])
		}
	} else if um, ok := dstVal.Addr().Interface().(ParameterUnmarshaler); ok {
		return um.UnmarshalParameter(srcPars[0])
	}

	return fmt.Errorf("unsupported field type")
}
