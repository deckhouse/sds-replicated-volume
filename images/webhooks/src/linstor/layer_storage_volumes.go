package linstor

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type LayerStorageVolumes struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		LayerResourceId int    `json:"layer_resource_id"`
		NodeName        string `json:"node_name"`
		ProviderKind    string `json:"provider_kind"`
		StorPoolName    string `json:"stor_pool_name"`
		VlmNr           int    `json:"vlm_nr"`
	} `json:"spec"`
}

type LayerStorageVolumesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LayerStorageVolumes `json:"items"`
}
