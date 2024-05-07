package linstor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ResourceGroups struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		AllowedProviderList   string `json:"allowed_provider_list"`
		Description           string `json:"description"`
		DoNotPlaceWithRscList string `json:"do_not_place_with_rsc_list"`
		LayerStack            string `json:"layer_stack"`
		NodeNameList          string `json:"node_name_list"`
		PoolName              string `json:"pool_name"`
		PoolNameDiskless      string `json:"pool_name_diskless"`
		ReplicaCount          int    `json:"replica_count"`
		ReplicasOnDifferent   string `json:"replicas_on_different"`
		ReplicasOnSame        string `json:"replicas_on_same"`
		ResourceGroupDspName  string `json:"resource_group_dsp_name"`
		ResourceGroupName     string `json:"resource_group_name"`
		Uuid                  string `json:"uuid"`
	} `json:"spec"`
}

type ResourceGroupsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ResourceGroups `json:"items"`
}
