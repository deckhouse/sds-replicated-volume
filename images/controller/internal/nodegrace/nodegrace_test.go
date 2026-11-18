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

package nodegrace_test

import (
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/nodegrace"
)

func TestIsReadyOrWithinGrace(t *testing.T) {
	transitionTime := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	gracePeriod := 5 * time.Minute
	expirationTime := transitionTime.Add(gracePeriod)

	tests := []struct {
		name                    string
		state                   nodegrace.ReadyState
		now                     time.Time
		wantNodeReady           bool
		wantNotReadyBeyondGrace bool
		wantGraceExpiresAt      time.Time
	}{
		{
			name: "ready",
			state: nodegrace.ReadyState{
				HasCondition: true,
				Status:       true,
			},
			now:           expirationTime.Add(time.Minute),
			wantNodeReady: true,
		},
		{
			name:  "no ready condition",
			state: nodegrace.ReadyState{Status: true},
			now:   expirationTime.Add(time.Minute),
		},
		{
			name: "not ready within grace",
			state: nodegrace.ReadyState{
				HasCondition:       true,
				LastTransitionTime: transitionTime,
			},
			now:                transitionTime.Add(2 * time.Minute),
			wantGraceExpiresAt: expirationTime,
		},
		{
			name: "not ready beyond grace",
			state: nodegrace.ReadyState{
				HasCondition:       true,
				LastTransitionTime: transitionTime,
			},
			now:                     transitionTime.Add(10 * time.Minute),
			wantNotReadyBeyondGrace: true,
		},
		{
			name: "exact expiration is within grace",
			state: nodegrace.ReadyState{
				HasCondition:       true,
				LastTransitionTime: transitionTime,
			},
			now:                expirationTime,
			wantGraceExpiresAt: expirationTime,
		},
		{
			name: "one nanosecond after expiration is beyond grace",
			state: nodegrace.ReadyState{
				HasCondition:       true,
				LastTransitionTime: transitionTime,
			},
			now:                     expirationTime.Add(time.Nanosecond),
			wantNotReadyBeyondGrace: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeReady, notReadyBeyondGrace, graceExpiresAt := nodegrace.IsReadyOrWithinGrace(tt.state, gracePeriod, tt.now)
			if nodeReady != tt.wantNodeReady {
				t.Errorf("nodeReady = %v, want %v", nodeReady, tt.wantNodeReady)
			}
			if notReadyBeyondGrace != tt.wantNotReadyBeyondGrace {
				t.Errorf("notReadyBeyondGrace = %v, want %v", notReadyBeyondGrace, tt.wantNotReadyBeyondGrace)
			}
			if !graceExpiresAt.Equal(tt.wantGraceExpiresAt) {
				t.Errorf("graceExpiresAt = %v, want %v", graceExpiresAt, tt.wantGraceExpiresAt)
			}
		})
	}
}

func TestNewReadyState(t *testing.T) {
	transitionTime := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		conditions []corev1.NodeCondition
		want       nodegrace.ReadyState
	}{
		{
			name: "no conditions",
		},
		{
			name: "no ready condition",
			conditions: []corev1.NodeCondition{
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
			},
		},
		{
			name: "true",
			conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(transitionTime)},
			},
			want: nodegrace.ReadyState{
				HasCondition:       true,
				Status:             true,
				LastTransitionTime: transitionTime,
			},
		},
		{
			name: "false",
			conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(transitionTime)},
			},
			want: nodegrace.ReadyState{
				HasCondition:       true,
				LastTransitionTime: transitionTime,
			},
		},
		{
			name: "unknown",
			conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionUnknown, LastTransitionTime: metav1.NewTime(transitionTime)},
			},
			want: nodegrace.ReadyState{
				HasCondition:       true,
				LastTransitionTime: transitionTime,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{Status: corev1.NodeStatus{Conditions: slices.Clone(tt.conditions)}}
			got := nodegrace.NewReadyState(node)
			if !slices.Equal(node.Status.Conditions, tt.conditions) {
				t.Error("NewReadyState modified the Node conditions")
			}

			// The snapshot must remain independent of the source Node.
			clear(node.Status.Conditions)
			if got.HasCondition != tt.want.HasCondition || got.Status != tt.want.Status || !got.LastTransitionTime.Equal(tt.want.LastTransitionTime) {
				t.Errorf("NewReadyState() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
