package linstor

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type PropsContainersSpec struct {
	PropKey       string `json:"prop_key"`
	PropValue     string `json:"prop_value"`
	PropsInstance string `json:"props_instance"`
}

type PropsContainers struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PropsContainersSpec `json:"spec"`
}

type PropsContainersList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []PropsContainers `json:"items"`
}
