package suite

type ClusterOptions struct {
	Nodes []NodeOptions `json:"nodes"`
}

type NodeOptions struct {
	Name    string `json:"name"`
	LVGName string `json:"lvgName,omitempty"` // optional
}
