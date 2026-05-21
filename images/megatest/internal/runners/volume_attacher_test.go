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

package runners

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestChooseRVAForSingleAttachmentPrefersNonDeletingAttachedRVA(t *testing.T) {
	deletionTime := metav1.Now()
	rvas := []v1alpha1.ReplicatedVolumeAttachment{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "deleting-attached",
				DeletionTimestamp: &deletionTime,
			},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Phase: v1alpha1.ReplicatedVolumeAttachmentPhaseAttached,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "active-attached"},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Phase: v1alpha1.ReplicatedVolumeAttachmentPhaseAttached,
			},
		},
	}

	got := chooseRVAForSingleAttachment(rvas)
	if got.Name != "active-attached" {
		t.Fatalf("expected active-attached, got %q", got.Name)
	}
}
