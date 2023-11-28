SHELL := /bin/bash
base_golang_20_bullseye := golang:1.20-bullseye
repo_local := localhost:5000
repo_fox := registry.flant.com/deckhouse/storage
image_name := sds-drbd


deployment_name=sds-drbd-controller
namespace=d8-sds-drbd
system_namespace=d8-system
pull_secret_name=fox-registry

USER := $(shell whoami)
image_tag := $(USER)

run: ## run go
	go run -race ./cmd/main.go

# local_build_and_push:
# 	docker build --build-arg="BASE_GOLANG_19_BULLSEYE=$(base_golang_19_bullseye)" . -t $(repo_local)/$(image_name):$(image_tag)
# 	docker push $(repo_local)/$(image_name):$(image_tag)

# offline_build_and_push:
# 	docker build --build-arg="BASE_GOLANG_19_BULLSEYE=localhost:5000/d8-sds-drbd-operator-builder" . -t $(repo_local)/$(image_name):$(image_tag)
# 	docker push $(repo_local)/$(image_name):$(image_tag)

fox_build_and_push:
	docker build --build-arg="BASE_GOLANG_20_BULLSEYE=$(base_golang_20_bullseye)" . -t $(repo_fox)/$(image_name):$(image_tag) -f dev/Dockerfile-dev
	docker push $(repo_fox)/$(image_name):$(image_tag)

# deploy_fox:
# 	sed 's/latest/$(USER)/g' ./dev/2-deployment-fox.yaml | kubectl -n $(namespace) apply -f -

# deploy_local:
# 	kubectl -n $(namespace) apply -f ./dev/1-deployment-local.yaml

redeploy:
	kubectl -n $(namespace) rollout restart deployment $(deployment_name)


switch_to_local_dev:
	kubectl -n $(system_namespace) scale deployment deckhouse --replicas=0
	kubectl -n $(namespace) patch deployment $(deployment_name) -p \
		'{"spec": {"template": {"spec": {"containers": [{"name": "$(deployment_name)", "image": "$(repo_fox)/$(image_name):$(image_tag)"}]}}}}'
	kubectl -n $(namespace) patch deployment $(deployment_name) -p \
		'{"spec": {"template": {"spec": {"containers": [{"name": "$(deployment_name)", "imagePullPolicy": "Always"}]}}}}'
	kubectl  -n $(namespace) patch deployment $(deployment_name) --type='json' \
		-p='[{"op": "add", "path": "/spec/template/spec/imagePullSecrets", "value": [{"name": "$(pull_secret_name)"}]}]'
.PHONY: switch_to_local_dev


