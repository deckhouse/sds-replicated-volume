package rvrstatusconditions

import "os"

const (
	// podNamespaceEnvVar is expected to be provided via Downward API in the controller Deployment.
	podNamespaceEnvVar = "POD_NAMESPACE"

	// agentNamespaceDefault matches the Helm namespace template: `d8-{{ .Chart.Name }}`.
	agentNamespaceDefault = "d8-sds-replicated-volume"
)

func agentNamespace() string {
	if ns := os.Getenv(podNamespaceEnvVar); ns != "" {
		return ns
	}
	return agentNamespaceDefault
}
