package drbdconf

import (
	"fmt"
	"reflect"
	"strconv"
)

var ParameterTypeCodecs = map[reflect.Type]ParameterTypeCodec{
	// TODO
	reflect.TypeFor[bool](): &boolParameterCodec{},
	reflect.TypeFor[*int](): &intPtrParameterCodec{},
}

type ParameterTypeCodec interface {
	MarshalParameter(v any) ([]string, error)
	UnmarshalParameter(p Parameter) (any, error)
}

type boolParameterCodec struct {
}

var _ ParameterTypeCodec = &boolParameterCodec{}

func (*boolParameterCodec) MarshalParameter(_ any) ([]string, error) {
	return nil, nil
}

func (*boolParameterCodec) UnmarshalParameter(_ Parameter) (any, error) {
	return true, nil
}

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
