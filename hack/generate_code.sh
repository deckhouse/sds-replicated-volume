#!/bin/bash

# Copyright 2025 Flant JSC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# run from repository root with: 'bash hack/generate_code.sh'
set -euo pipefail
# Both generators are pinned in hack/tools/go.mod and built into a temporary
# directory.
#
# controller-gen used to be run as `go run -mod=mod ...@v0.19` from inside api/,
# which resolved to whatever v0.19.x was current, wrote the tool's dependencies
# into api/go.sum and then needed a `go mod tidy` to take them back out again.
#
# deepcopy-gen used to be run as `go run k8s.io/code-generator/...` from inside
# api/, where it is not a dependency at all: the package resolved only because
# some other member of go.work happens to require k8s.io/code-generator, so the
# version generating zz_generated.deepcopy.go was whatever an unrelated module
# pinned. Generated code has to come from a version this repository chose.
TOOLS_DIR="$(mktemp -d)"
trap 'rm -rf "${TOOLS_DIR}"' EXIT
GOWORK=off go -C hack/tools build -o "${TOOLS_DIR}" \
    sigs.k8s.io/controller-tools/cmd/controller-gen \
    k8s.io/code-generator/cmd/deepcopy-gen

cd api

"${TOOLS_DIR}/deepcopy-gen" -v 2 \
    --output-file zz_generated.deepcopy.go \
    --go-header-file ../hack/boilerplate.txt \
    ./linstor

"${TOOLS_DIR}/controller-gen" object:headerFile=../hack/boilerplate.txt paths=./v1alpha1

cd ..

# The CRD manifests are rendered and then enriched; that pair lives in its own
# script because the shared .crds_generated_check CI job runs it by name.
./hack/generate_crds.sh

# validate generated CRDs (CEL cost budget, schema correctness)
(cd api && go run ../hack/validate-crds.go ../crds)

# generate mocks and any other go:generate targets across all modules
./hack/for-each-mod go generate ./...

# TODO: re-generate spec according to changes in CRDs with AI

echo "OK"
