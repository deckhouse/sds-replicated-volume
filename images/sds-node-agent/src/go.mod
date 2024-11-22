module sds-node-agent

go 1.22.7

require (
	github.com/go-logr/logr v1.4.2
	k8s.io/klog/v2 v2.130.1
)

replace github.com/deckhouse/sds-replicated-volume/api => ../../../api
