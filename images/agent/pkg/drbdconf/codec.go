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
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

var parameterTypeCodecs = map[reflect.Type]ParameterTypeCodec{
	reflect.TypeFor[[]string](): &stringSliceParameterCodec{},
	reflect.TypeFor[string]():   &stringParameterCodec{},
	reflect.TypeFor[bool]():     &boolParameterCodec{},
	reflect.TypeFor[*bool]():    &boolPtrParameterCodec{},
	reflect.TypeFor[*int]():     &intPtrParameterCodec{},
	reflect.TypeFor[*uint]():    &uintPtrParameterCodec{},
}

var parameterTypeCodecsMu = &sync.Mutex{}

func RegisterParameterTypeCodec[T any](codec ParameterTypeCodec) {
	parameterTypeCodecsMu.Lock()
	defer parameterTypeCodecsMu.Unlock()
	parameterTypeCodecs[reflect.TypeFor[T]()] = codec
}

type ParameterTypeCodec interface {
	MarshalParameter(v any) ([]string, error)
	UnmarshalParameter(p []Word) (any, error)
}

// ======== [string] ========

type stringParameterCodec struct {
}

var _ ParameterTypeCodec = &stringParameterCodec{}

func (c *stringParameterCodec) MarshalParameter(v any) ([]string, error) {
	return []string{v.(string)}, nil
}

func (*stringParameterCodec) UnmarshalParameter(p []Word) (any, error) {
	if err := EnsureLen(p, 2); err != nil {
		return nil, err
	}
	return p[1].Value, nil
}

// ======== [[]string] ========

type stringSliceParameterCodec struct {
}

var _ ParameterTypeCodec = &stringSliceParameterCodec{}

func (c *stringSliceParameterCodec) MarshalParameter(v any) ([]string, error) {
	return v.([]string), nil
}

func (*stringSliceParameterCodec) UnmarshalParameter(par []Word) (any, error) {
	res := []string{}
	for i := 1; i < len(par); i++ {
		res = append(res, par[i].Value)
	}
	return res, nil
}

// ======== [bool] ========

type boolParameterCodec struct {
}

var _ ParameterTypeCodec = &boolParameterCodec{}

func (*boolParameterCodec) MarshalParameter(_ any) ([]string, error) {
	return nil, nil
}

func (*boolParameterCodec) UnmarshalParameter(_ []Word) (any, error) {
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

func (*boolPtrParameterCodec) UnmarshalParameter(par []Word) (any, error) {
	if strings.HasPrefix(par[0].Value, "no-") && len(par) == 1 {
		return ptr(false), nil
	}

	if len(par) == 1 || par[1].Value == "yes" {
		return ptr(true), nil
	}

	if par[1].Value == "no" {
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

func (*intPtrParameterCodec) UnmarshalParameter(p []Word) (any, error) {
	if err := EnsureLen(p, 2); err != nil {
		return nil,
			fmt.Errorf("unmarshaling '%s' to *int: %w", p[0].Value, err)
	}

	i, err := strconv.Atoi(p[1].Value)
	if err != nil {
		return nil,
			fmt.Errorf(
				"unmarshaling '%s' value to *int: %w",
				p[0].Value, err,
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

func (*uintPtrParameterCodec) UnmarshalParameter(p []Word) (any, error) {
	if err := EnsureLen(p, 2); err != nil {
		return nil,
			fmt.Errorf("unmarshaling '%s' to *uint: %w", p[0].Value, err)
	}

	i64, err := strconv.ParseUint(p[1].Value, 10, 0)
	if err != nil {
		return nil,
			fmt.Errorf(
				"unmarshaling '%s' value to *int: %w",
				p[0].Value, err,
			)
	}

	i := uint(i64)

	return &i, nil
}
