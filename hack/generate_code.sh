#!/bin/bash

# run from repository root with: 'bash hack/generate_code.sh'
set -e
cd api

# crds
go get sigs.k8s.io/controller-tools/cmd/controller-gen

go run sigs.k8s.io/controller-tools/cmd/controller-gen \
    crd paths=./v1alpha3 output:crd:dir=../crds

# deep copy

go get k8s.io/code-generator/cmd/deepcopy-gen

go run k8s.io/code-generator/cmd/deepcopy-gen -v 2 \
    --output-file zz_generated.deepcopy.go \
    --go-header-file ../hack/boilerplate.txt \
    ./v1alpha1

go run k8s.io/code-generator/cmd/deepcopy-gen -v 2 \
    --output-file zz_generated.deepcopy.go \
    --go-header-file ../hack/boilerplate.txt \
    ./v1alpha3

# remove development dependencies
go mod tidy

cd ..

# generate mocks and any other go:generate targets across all modules
./hack/for-each-mod go generate ./...

# TODO: re-generate spec according to changes in CRDs with AI

echo "OK"