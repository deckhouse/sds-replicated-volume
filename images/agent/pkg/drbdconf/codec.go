package drbdconf

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

var ParameterTypeCodecs = map[reflect.Type]ParameterTypeCodec{
	// TODO
	reflect.TypeFor[string](): &stringParameterCodec{},
	reflect.TypeFor[bool]():   &boolParameterCodec{},
	reflect.TypeFor[*bool]():  &boolPtrParameterCodec{},
	reflect.TypeFor[*int]():   &intPtrParameterCodec{},
	reflect.TypeFor[*uint]():  &uintPtrParameterCodec{},
}

type ParameterTypeCodec interface {
	MarshalParameter(v any) ([]string, error)
	UnmarshalParameter(p Parameter) (any, error)
}

// ======== [string] ========

type stringParameterCodec struct {
}

var _ ParameterTypeCodec = &stringParameterCodec{}

func (c *stringParameterCodec) MarshalParameter(v any) ([]string, error) {
	return []string{v.(string)}, nil
}

func (*stringParameterCodec) UnmarshalParameter(_ Parameter) (any, error) {
	panic("TODO")
}

// ======== [bool] ========

type boolParameterCodec struct {
}

var _ ParameterTypeCodec = &boolParameterCodec{}

func (*boolParameterCodec) MarshalParameter(_ any) ([]string, error) {
	return nil, nil
}

func (*boolParameterCodec) UnmarshalParameter(_ Parameter) (any, error) {
	return true, nil
}

// ======== [*bool] ========

type boolPtrParameterCodec struct {
}

var _ ParameterTypeCodec = &boolPtrParameterCodec{}

func (*boolPtrParameterCodec) MarshalParameter(v any) ([]string, error) {
	if *(v.(*bool)) {
		return []string{"yes"}, nil
	} else {
		return []string{"no"}, nil
	}
}

func (*boolPtrParameterCodec) UnmarshalParameter(par Parameter) (any, error) {
	if strings.HasPrefix(par.Key[0].Value, "no-") && len(par.Key) == 1 {
		return ptr(false), nil
	}

	if len(par.Key) == 1 || par.Key[1].Value == "yes" {
		return ptr(true), nil
	}

	if par.Key[1].Value == "no" {
		return ptr(false), nil
	}

	return nil, fmt.Errorf("format error: expected 'yes' or 'no'")
}

// ======== [*int] ========

type intPtrParameterCodec struct {
}

var _ ParameterTypeCodec = &intPtrParameterCodec{}

func (*intPtrParameterCodec) MarshalParameter(v any) ([]string, error) {
	return []string{strconv.Itoa(*(v.(*int)))}, nil
}

func (*intPtrParameterCodec) UnmarshalParameter(p Parameter) (any, error) {
	if err := ensureLen(p.Key, 2); err != nil {
		return nil,
			fmt.Errorf("unmarshaling '%s' to *int: %w", p.Key[0].Value, err)
	}

	i, err := strconv.Atoi(p.Key[1].Value)
	if err != nil {
		return nil,
			fmt.Errorf(
				"unmarshaling '%s' value to *int: %w",
				p.Key[0].Value, err,
			)
	}

	return &i, nil
}

// ======== [*uint] ========

type uintPtrParameterCodec struct {
}

var _ ParameterTypeCodec = &uintPtrParameterCodec{}

func (*uintPtrParameterCodec) MarshalParameter(v any) ([]string, error) {
	return []string{strconv.FormatUint(uint64(*(v.(*uint))), 10)}, nil
}

func (*uintPtrParameterCodec) UnmarshalParameter(p Parameter) (any, error) {
	if err := ensureLen(p.Key, 2); err != nil {
		return nil,
			fmt.Errorf("unmarshaling '%s' to *uint: %w", p.Key[0].Value, err)
	}

	i64, err := strconv.ParseUint(p.Key[1].Value, 10, 0)
	if err != nil {
		return nil,
			fmt.Errorf(
				"unmarshaling '%s' value to *int: %w",
				p.Key[0].Value, err,
			)
	}

	i := uint(i64)

	return &i, nil
}
