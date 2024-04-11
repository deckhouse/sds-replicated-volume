package linstor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Resources struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		CreateTimestamp int64  `json:"create_timestamp"`
		NodeName        string `json:"node_name"`
		ResourceFlags   int    `json:"resource_flags"`
		ResourceName    string `json:"resource_name"`
		SnapshotName    string `json:"snapshot_name"`
		Uuid            string `json:"uuid"`
	} `json:"spec"`
}

type ResourcesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Resources `json:"items"`
}
