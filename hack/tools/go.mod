// Code-generation tools for this repository, pinned here rather than in
// api/go.mod: controller-tools drags k8s.io/apiextensions-apiserver and
// k8s.io/code-generator into whatever module requires it, and api/ is imported
// by the images, so pinning them there would bump the dependencies that
// production code is built against. Generation used to sidestep that with
// `go run -mod=mod ...@v0.19` followed by `go mod tidy`, which churned api/go.sum
// on every run and left the exact version to whatever v0.19.x resolved to.
//
// The pinned versions are the whole point of this module: the CRDs in crds/ are
// a generated artifact, so the generator is part of their definition and a
// floating version would make the committed manifests depend on when they were
// last rendered. Bump with, from this directory:
//
//	go get -tool sigs.k8s.io/controller-tools/cmd/controller-gen@vX.Y.Z
//	go get -tool github.com/deckhouse/deckhouse/pkg/crd-enricher/cmd/crd-enricher@vX.Y.Z
//
// and regenerate: a bump that changes the rendered output shows up as a diff in
// crds/ in the same commit, which is exactly where it should be reviewed.
//
// controller-tools stays on v0.19.0 here while the other storage modules pin
// v0.18.0. That is deliberate and not drift: generation in this repository has
// been running `controller-gen@v0.19` all along, so pinning v0.18.0 would rewrite
// the committed manifests as a side effect of introducing the pin. Aligning the
// fleet is its own change, and it has to move every module at once.
module sds-replicated-volume-codegen-tools

go 1.27.3
tool (
	github.com/deckhouse/deckhouse/pkg/crd-enricher/cmd/crd-enricher
	k8s.io/code-generator/cmd/deepcopy-gen
	sigs.k8s.io/controller-tools/cmd/controller-gen
)

require (
	github.com/deckhouse/deckhouse/pkg/crd-enricher v0.0.3-0.20260825165152-c9d98f54c23d // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/gobuffalo/flect v1.0.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/spf13/cobra v1.9.1 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.34.0 // indirect
	k8s.io/apiextensions-apiserver v0.34.0 // indirect
	k8s.io/apimachinery v0.35.0 // indirect
	k8s.io/code-generator v0.35.0 // indirect
	k8s.io/gengo/v2 v2.0.0-20250922181213-ec3ebc5fd46b // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20250910181357-589584f1c912 // indirect
	k8s.io/utils v0.0.0-20251002143259-bc988d571ff4 // indirect
	sigs.k8s.io/controller-tools v0.19.0 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
