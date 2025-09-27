package cluster

// This file declares go:generate directives to produce mocks using Uber's mockgen
// for interfaces used by the Cluster during unit tests.
//
// To regenerate mocks, run from the repository root or this package dir:
//   go generate ./images/controller/internal/reconcile/rv/cluster

// Mocks for interfaces declared in cluster.go
//go:generate go run go.uber.org/mock/mockgen@latest -destination=mock_rvr_client.go -package=cluster . RVRClient
//go:generate go run go.uber.org/mock/mockgen@latest -destination=mock_llv_client.go -package=cluster . LLVClient
//go:generate go run go.uber.org/mock/mockgen@latest -destination=mock_port_manager.go -package=cluster . PortManager
//go:generate go run go.uber.org/mock/mockgen@latest -destination=mock_minor_manager.go -package=cluster . MinorManager

// Mocks for interfaces declared in resource_manager.go
//go:generate go run go.uber.org/mock/mockgen@latest -destination=mock_node_rvr_client.go -package=cluster . NodeRVRClient
//go:generate go run go.uber.org/mock/mockgen@latest -destination=mock_drbd_port_range.go -package=cluster . DRBDPortRange
