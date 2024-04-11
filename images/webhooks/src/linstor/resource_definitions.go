package linstor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ResourceDefinitions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		LayerStack        string `json:"layer_stack"`
		ResourceDspName   string `json:"resource_dsp_name"`
		ResourceFlags     int    `json:"resource_flags"`
		ResourceGroupName string `json:"resource_group_name"`
		ResourceName      string `json:"resource_name"`
		SnapshotDspName   string `json:"snapshot_dsp_name"`
		SnapshotName      string `json:"snapshot_name"`
		Uuid              string `json:"uuid"`
	} `json:"spec"`
}

type ResourceDefinitionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ResourceDefinitions `json:"items"`
}
