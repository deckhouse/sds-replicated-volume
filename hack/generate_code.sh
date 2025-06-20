#!/bin/bash

# run from repository root with: 'bash hack/generate_code.sh'
set -e
cd api

# crds
go get sigs.k8s.io/controller-tools/cmd/controller-gen

go run sigs.k8s.io/controller-tools/cmd/controller-gen \
    crd paths=./v1alpha2 output:crd:dir=../crds

# deep copy

go get k8s.io/code-generator/cmd/deepcopy-gen

go run k8s.io/code-generator/cmd/deepcopy-gen -v 2 \
    --output-file zz_generated.deepcopy.go \
    --go-header-file ../hack/boilerplate.txt \
    ./v1alpha1

go run k8s.io/code-generator/cmd/deepcopy-gen -v 2 \
    --output-file zz_generated.deepcopy.go \
    --go-header-file ../hack/boilerplate.txt \
    ./v1alpha2

# remove development dependencies
go mod tidy

cd ..

echo "OK"