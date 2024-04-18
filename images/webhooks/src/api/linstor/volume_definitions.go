package linstor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VolumeDefinitions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		ResourceName string `json:"resource_name"`
		SnapshotName string `json:"snapshot_name"`
		Uuid         string `json:"uuid"`
		VlmFlags     int    `json:"vlm_flags"`
		VlmNr        int    `json:"vlm_nr"`
		VlmSize      int    `json:"vlm_size"`
	} `json:"spec"`
}

type VolumeDefinitionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []VolumeDefinitions `json:"items"`
}
