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
set -e
cd api

# crds
go get sigs.k8s.io/controller-tools/cmd/controller-gen

go run sigs.k8s.io/controller-tools/cmd/controller-gen \
    crd paths=./v1alpha1 output:crd:dir=../crds

# deep copy

go get k8s.io/code-generator/cmd/deepcopy-gen

go run k8s.io/code-generator/cmd/deepcopy-gen -v 2 \
    --output-file zz_generated.deepcopy.go \
    --go-header-file ../hack/boilerplate.txt \
    ./v1alpha1

# remove development dependencies
go mod tidy

cd ..

# generate mocks and any other go:generate targets across all modules
./hack/for-each-mod go generate ./...

# TODO: re-generate spec according to changes in CRDs with AI

echo "OK"