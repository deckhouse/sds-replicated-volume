package linstor

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type LayerDrbdVolumeDefinitions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		ResourceName       string `json:"resource_name"`
		ResourceNameSuffix string `json:"resource_name_suffix"`
		SnapshotName       string `json:"snapshot_name"`
		VlmMinorNr         int    `json:"vlm_minor_nr"`
		VlmNr              int    `json:"vlm_nr"`
	} `json:"spec"`
}

type LayerDrbdVolumeDefinitionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LayerDrbdVolumeDefinitions `json:"items"`
}
