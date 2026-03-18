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

package framework

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// GVKs for all e2e-managed Kubernetes types.
var (
	gvkRV    = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolume")
	gvkRVR   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeReplica")
	gvkRVA   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeAttachment")
	gvkDRBDR = v1alpha1.SchemeGroupVersion.WithKind("DRBDResource")
	gvkRSC   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedStorageClass")
	gvkRSP   = v1alpha1.SchemeGroupVersion.WithKind("ReplicatedStoragePool")
	gvkLLV   = snc.SchemeGroupVersion.WithKind("LVMLogicalVolume")
)

// e2eTypes returns the object types managed by e2e tests, in the order
// they should be deleted (attachments first, then volumes, etc.).
func e2eTypes() []client.Object {
	return []client.Object{
		&v1alpha1.ReplicatedVolumeAttachment{},
		&v1alpha1.ReplicatedVolume{},
		&v1alpha1.ReplicatedVolumeReplica{},
		&v1alpha1.DRBDResource{},
		&snc.LVMLogicalVolume{},
		&v1alpha1.ReplicatedStorageClass{},
	}
}

// e2eListTypes returns fresh ObjectList instances for each e2e-managed type.
func e2eListTypes() []client.ObjectList {
	return []client.ObjectList{
		&v1alpha1.ReplicatedVolumeAttachmentList{},
		&v1alpha1.ReplicatedVolumeList{},
		&v1alpha1.ReplicatedVolumeReplicaList{},
		&v1alpha1.DRBDResourceList{},
		&snc.LVMLogicalVolumeList{},
		&v1alpha1.ReplicatedStorageClassList{},
	}
}
