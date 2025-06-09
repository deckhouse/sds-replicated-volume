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

package linstor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	APIGroup      = "internal.linstor.linbit.com"
	APIVersion    = "v1-15-0"
	crdGroup      = "internal.linstor.linbit.com"
	crdNamePlural = "propscontainers"
	crdVersion    = "v1-15-0"
)

// SchemeGroupVersion is group version used to register these objects
var (
	SchemeGroupVersionGVR = schema.GroupVersionResource{
		Group:    crdGroup,
		Version:  crdVersion,
		Resource: crdNamePlural,
	}
	SchemeGroupVersion = schema.GroupVersion{
		Group:   APIGroup,
		Version: APIVersion,
	}

	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

// Adds the list of known types to Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&ResourceGroups{},
		&ResourceGroupsList{},
		&LayerDrbdResourceDefinitions{},
		&LayerDrbdResourceDefinitionsList{},
		&VolumeDefinitions{},
		&VolumeDefinitionsList{},
		&LayerDrbdVolumeDefinitions{},
		&LayerDrbdVolumeDefinitionsList{},
		&PropsContainers{},
		&PropsContainersList{},
		&Resources{},
		&ResourcesList{},
		&LayerStorageVolumes{},
		&LayerStorageVolumesList{},
		&ResourceDefinitions{},
		&ResourceDefinitionsList{},
	)

	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)

	return nil
}
