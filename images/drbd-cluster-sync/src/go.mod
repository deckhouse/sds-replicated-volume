module drbd-cluster-sync

go 1.23.3

require (
	github.com/LINBIT/golinstor v0.52.2
	github.com/avast/retry-go v3.0.0+incompatible
	github.com/deckhouse/sds-replicated-volume/api v0.0.0-20241225145836-3d4baa215eaf
	k8s.io/apimachinery v0.32.1
)

replace github.com/deckhouse/sds-replicated-volume/api => ../../../api

require (
	github.com/container-storage-interface/spec v1.10.0 // indirect
	github.com/donovanhide/eventsource v0.0.0-20210830082556-c59027999da0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/haySwim/data v0.2.0 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/opencontainers/runc v1.2.0 // indirect
	github.com/pborman/uuid v1.2.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/exp v0.0.0-20240719175910-8a7402abbf56 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142 // indirect
	google.golang.org/grpc v1.67.1 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	k8s.io/mount-utils v0.31.2 // indirect
	moul.io/http2curl/v2 v2.3.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.12.1 // indirect
	github.com/evanphx/json-patch/v5 v5.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.9-0.20230804172637-c7be7c783f49 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/piraeusdatastore/linstor-csi v1.6.4
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.3
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/net v0.33.0 // indirect
	golang.org/x/oauth2 v0.23.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	golang.org/x/term v0.27.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	golang.org/x/time v0.9.0
	google.golang.org/protobuf v1.35.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.32.1
	k8s.io/client-go v0.32.1
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	k8s.io/utils v0.0.0-20241104100929-3ea5e8cea738 // indirect
	sigs.k8s.io/controller-runtime v0.19.3
	sigs.k8s.io/json v0.0.0-20241010143419-9aa6b5e7a4b3 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.2 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)
