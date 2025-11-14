package drbdconf

import "fmt"

func SectionKeyword[T any, TP SectionPtr[T]]() string {
	return TP(nil).SectionKeyword()
}

func ptr[T any](v T) *T { return &v }

func EnsureLen(words []Word, lenAtLeast int) error {
	if len(words) < lenAtLeast {
		var loc Location
		if len(words) > 0 {
			loc = words[len(words)-1].LocationEnd()
		}
		return fmt.Errorf("%s: missing value", loc)
	}

	return nil
}

func ReadEnum[T ~string](
	dst *T,
	knownValues map[T]struct{},
	value string,
) error {
	if err := EnsureEnum(knownValues, value); err != nil {
		return err
	}
	*dst = T(value)
	return nil
}

func ReadEnumAt[T ~string](
	dst *T,
	knownValues map[T]struct{},
	p []Word,
	idx int,
) error {
	if err := EnsureLen(p, idx+1); err != nil {
		return err
	}
	if err := ReadEnum(dst, knownValues, p[idx].Value); err != nil {
		return err
	}
	*dst = T(p[idx].Value)
	return nil
}

func EnsureEnum[T ~string](knownValues map[T]struct{}, value string) error {
	if _, ok := knownValues[T(value)]; !ok {
		return fmt.Errorf("unrecognized value: '%s'", value)
	}
	return nil
}
