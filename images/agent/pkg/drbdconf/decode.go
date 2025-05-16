package drbdconf

import (
	"fmt"
	"reflect"
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
	if !isNonNilStructPtr(ptrVal) {
		return fmt.Errorf("expected non-nil pointer to a struct")
	}

	err := visitStructFields(
		ptrVal,
		func(f *visitedField) error {

			if len(f.ParameterNames) > 0 {

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
