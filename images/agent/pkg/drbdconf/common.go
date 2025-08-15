package drbdconf

import (
	"fmt"
	"reflect"
)

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

func isStructPtrAndSectionKeyworder(v reflect.Value) (ok bool, kw string) {
	ok = isNonNilStructPtr(v) &&
		v.Type().Implements(reflect.TypeFor[SectionKeyworder]())
	if ok {
		kw = v.Interface().(SectionKeyworder).SectionKeyword()
	}
	return
}

func isNonNilStructPtr(v reflect.Value) bool {
	return v.Kind() == reflect.Pointer &&
		!v.IsNil() &&
		v.Elem().Kind() == reflect.Struct
}

func isSectionKeyworder(v reflect.Value) bool {
	return v.Type().Implements(reflect.TypeFor[SectionKeyworder]())
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
