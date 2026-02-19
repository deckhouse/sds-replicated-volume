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

set -e

cd images/agent
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ./out ./cmd
rm -f ./out
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./...
echo "agent ok"
cd - > /dev/null

cd images/controller
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ./out ./cmd
rm -f ./out
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./...
echo "controller ok"
cd - > /dev/null

cd images/csi-driver
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ./out ./cmd
rm -f ./out
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./...
echo "csi-driver ok"
cd - > /dev/null

cd api
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ./out ./v1alpha1
rm -f ./out
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./...
echo "api ok"
cd - > /dev/null

cd images/webhooks
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ./out ./cmd
rm -f ./out
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./...
echo "webhooks ok"
cd - > /dev/null

