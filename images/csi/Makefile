SHELL := /bin/bash
TEST-ARGS=-race -timeout 30s -count 1
base_golang_21_alpine := golang:1.21.4-alpine3.18
repo_fox := registry.flant.com/deckhouse/storage
image_name := sds-local-volume


deployment_name=sds-local-volume-csi-controller
daemonset_name=sds-local-volume-csi-node
deployment_container_name=sds-local-volume-csi-plugin
daemonset_container_name=sds-local-volume-csi-plugin
namespace=d8-sds-local-volume
system_namespace=d8-system
pull_secret_name=fox-registry

USER := $(shell whoami)
image_tag := $(USER)

run: ## run go
	go run -race ./cmd/main.go

test:
	go test $(TEST-ARGS) ./...

fox_build_and_push:
	docker build --build-arg="BASE_GOLANG_21_ALPINE_BUILDER=$(base_golang_21_alpine)" . -t $(repo_fox)/$(image_name):$(image_tag)
	docker push $(repo_fox)/$(image_name):$(image_tag)

redeploy:
	# kubectl -n $(namespace) rollout restart deployment $(deployment_name)
	kubectl -n $(namespace) rollout restart daemonset $(daemonset_name)

switch_to_local_dev:
	kubectl -n $(system_namespace) scale deployment deckhouse --replicas=0
	kubectl -n $(namespace) patch deployment $(deployment_name) -p \
		'{"spec": {"template": {"spec": {"containers": [{"name": "$(deployment_container_name)", "image": "$(repo_fox)/$(image_name):$(image_tag)"}]}}}}'
	kubectl -n $(namespace) patch deployment $(deployment_name) -p \
		'{"spec": {"template": {"spec": {"containers": [{"name": "$(deployment_container_name)", "imagePullPolicy": "Always"}]}}}}'
	kubectl  -n $(namespace) patch deployment $(deployment_name) --type='json' \
		-p='[{"op": "add", "path": "/spec/template/spec/imagePullSecrets", "value": [{"name": "$(pull_secret_name)"}]}]'

	kubectl -n $(namespace) patch daemonset $(daemonset_name) -p \
		'{"spec": {"template": {"spec": {"containers": [{"name": "$(daemonset_container_name)", "image": "$(repo_fox)/$(image_name):$(image_tag)"}]}}}}'
	kubectl -n $(namespace) patch daemonset $(daemonset_name) -p \
		'{"spec": {"template": {"spec": {"containers": [{"name": "$(daemonset_container_name)", "imagePullPolicy": "Always"}]}}}}'
	kubectl  -n $(namespace) patch daemonset $(daemonset_name) --type='json' \
		-p='[{"op": "add", "path": "/spec/template/spec/imagePullSecrets", "value": [{"name": "$(pull_secret_name)"}]}]'


.PHONY: switch_to_local_dev
