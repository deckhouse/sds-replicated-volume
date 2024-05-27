package linstor

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type LayerDrbdResourceDefinitions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		AlStripeSize       int    `json:"al_stripe_size"`
		AlStripes          int    `json:"al_stripes"`
		PeerSlots          int    `json:"peer_slots"`
		ResourceName       string `json:"resource_name"`
		ResourceNameSuffix string `json:"resource_name_suffix"`
		Secret             string `json:"secret"`
		SnapshotName       string `json:"snapshot_name"`
		TcpPort            int    `json:"tcp_port"`
		TransportType      string `json:"transport_type"`
	} `json:"spec"`
}

type LayerDrbdResourceDefinitionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LayerDrbdResourceDefinitions `json:"items"`
}
