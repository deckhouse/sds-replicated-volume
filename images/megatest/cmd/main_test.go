/*
Copyright 2026 Flant JSC

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

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
)

func TestClassifyCheckerStats(t *testing.T) {
	tests := []struct {
		name            string
		fttTransitions  int64
		gmdrTransitions int64
		fttStatus       string
		gmdrStatus      string
		want            checkerStatsCategory
	}{
		{
			name:       "stable",
			fttStatus:  "healthy",
			gmdrStatus: "healthy",
			want:       checkerStatsCategoryStable,
		},
		{
			name:            "recovered after unknown",
			fttTransitions:  1,
			gmdrTransitions: 0,
			fttStatus:       "healthy",
			gmdrStatus:      "healthy",
			want:            checkerStatsCategoryRecovered,
		},
		{
			name:       "broken final health",
			fttStatus:  "unhealthy",
			gmdrStatus: "healthy",
			want:       checkerStatsCategoryBroken,
		},
		{
			name:       "unknown final health",
			fttStatus:  "healthy",
			gmdrStatus: "unknown",
			want:       checkerStatsCategoryUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyCheckerStats(tt.fttTransitions, tt.gmdrTransitions, tt.fttStatus, tt.gmdrStatus)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestResolveRVSizeConfig(t *testing.T) {
	tests := []struct {
		name    string
		opt     Opt
		want    config.RVSizeConfig
		wantErr bool
	}{
		{
			name: "default fixed size",
			opt: Opt{
				RVSize:     defaultRVSize,
				RVSizeStep: defaultRVSizeStep,
			},
			want: config.RVSizeConfig{MinMi: 100, MaxMi: 100, StepMi: 100},
		},
		{
			name: "custom fixed size",
			opt: Opt{
				RVSize:     "1Gi",
				RVSizeStep: defaultRVSizeStep,
			},
			want: config.RVSizeConfig{MinMi: 1024, MaxMi: 1024, StepMi: 100},
		},
		{
			name: "random range",
			opt: Opt{
				RVSize:     "500Mi",
				RVSizeMin:  "100Mi",
				RVSizeStep: "100Mi",
			},
			want: config.RVSizeConfig{MinMi: 100, MaxMi: 500, StepMi: 100},
		},
		{
			name: "min greater than max fails",
			opt: Opt{
				RVSize:     defaultRVSize,
				RVSizeMin:  "500Mi",
				RVSizeStep: defaultRVSizeStep,
			},
			wantErr: true,
		},
		{
			name: "size below minimum fails",
			opt: Opt{
				RVSize:     "99Mi",
				RVSizeStep: defaultRVSizeStep,
			},
			wantErr: true,
		},
		{
			name: "non Mi size fails",
			opt: Opt{
				RVSize:     defaultRVSize,
				RVSizeStep: "1500Ki",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.opt.ResolveRVSizeConfig()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %#v, got %#v", tt.want, got)
			}
		})
	}
}

func TestDescribeRVSize(t *testing.T) {
	cases := []struct {
		name string
		size config.RVSizeConfig
		want string
	}{
		{
			name: "fixed",
			size: config.RVSizeConfig{MinMi: 100, MaxMi: 100, StepMi: 100},
			want: "fixed 100Mi",
		},
		{
			name: "range",
			size: config.RVSizeConfig{MinMi: 100, MaxMi: 500, StepMi: 100},
			want: "random 100Mi..500Mi, step 100Mi, inclusive endpoints",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeRVSize(tt.size); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestPrintStartupSummaryIncludesRVSize(t *testing.T) {
	cfg := config.MultiVolumeConfig{
		StorageClasses: []string{"sc-a"},
		MaxVolumes:     1,
		VolumeStep:     config.StepMinMax{Min: 1, Max: 1},
		RVSize:         config.RVSizeConfig{MinMi: 100, MaxMi: 500, StepMi: 100},
		StepPeriod:     config.DurationMinMax{Min: time.Second, Max: 2 * time.Second},
		VolumePeriod:   config.DurationMinMax{Min: time.Minute, Max: 5 * time.Minute},
	}

	var out bytes.Buffer
	printStartupSummary(&out, Opt{}, cfg)

	if !strings.Contains(out.String(), "random 100Mi..500Mi, step 100Mi, inclusive endpoints") {
		t.Fatalf("expected RV size summary, got:\n%s", out.String())
	}
}
