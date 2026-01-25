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

package testhelpers

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

// WithLLVByRVROwnerIndex registers the IndexFieldLLVByRVROwner index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithLLVByRVROwnerIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&snc.LVMLogicalVolume{}, indexes.IndexFieldLLVByRVROwner, func(obj client.Object) []string {
		llv, ok := obj.(*snc.LVMLogicalVolume)
		if !ok {
			return nil
		}
		ownerRef := metav1.GetControllerOf(llv)
		if ownerRef == nil || ownerRef.Kind != "ReplicatedVolumeReplica" {
			return nil
		}
		return []string{ownerRef.Name}
	})
}
