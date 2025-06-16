#!/bin/bash

cd ./api/

controller-gen crd paths=./v1alpha2 output:crd:dir=../crds

cd ..