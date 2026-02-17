package suite

type ClusterOptions struct {
	Nodes        []NodeOptions `json:"nodes"`
	AllocateSize string        `json:"allocateSize"`
}

type NodeOptions struct {
	Name    string `json:"name"`
	LVGName string `json:"lvgName,omitempty"` // optional
}
