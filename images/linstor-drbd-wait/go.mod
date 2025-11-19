module github.com/sds-replicated-volume/images/linstor-drbd-wait

go 1.24.6

require github.com/deckhouse/sds-replicated-volume/lib/go/common v0.0.0-00010101000000-000000000000

require (
	github.com/go-logr/logr v1.4.3 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
)

replace github.com/deckhouse/sds-replicated-volume/api => ../../api

replace github.com/deckhouse/sds-replicated-volume/lib/go/common => ../../lib/go/common
