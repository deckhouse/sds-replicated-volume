package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type LvmVolumeGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []LvmVolumeGroup `json:"items"`
}

type LvmVolumeGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LvmVolumeGroupSpec   `json:"spec"`
	Status LvmVolumeGroupStatus `json:"status,omitempty"`
}

type SpecThinPool struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

type LvmVolumeGroupSpec struct {
	ActualVGNameOnTheNode string         `json:"actualVGNameOnTheNode"`
	BlockDeviceNames      []string       `json:"blockDeviceNames"`
	ThinPools             []SpecThinPool `json:"thinPools"`
	Type                  string         `json:"type"`
}

type LvmVolumeGroupDevice struct {
	BlockDevice string `json:"blockDevice"`
	DevSize     string `json:"devSize"`
	PVSize      string `json:"pvSize"`
	PVUuid      string `json:"pvUUID"`
	Path        string `json:"path"`
}

type LvmVolumeGroupNode struct {
	Devices []LvmVolumeGroupDevice `json:"devices"`
	Name    string                 `json:"name"`
}

type StatusThinPool struct {
	Name       string `json:"name"`
	ActualSize string `json:"actualSize"`
	UsedSize   string `json:"usedSize"`
}

type LvmVolumeGroupStatus struct {
	AllocatedSize string               `json:"allocatedSize"`
	Health        string               `json:"health"`
	Message       string               `json:"message"`
	Nodes         []LvmVolumeGroupNode `json:"nodes"`
	ThinPools     []StatusThinPool     `json:"thinPools"`
	VGSize        string               `json:"vgSize"`
	VGUuid        string               `json:"vgUUID"`
}
