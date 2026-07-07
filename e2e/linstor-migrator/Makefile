# Makefile for linstor-migrator E2E tests (storage-e2e; manual runs only).
#
# This directory is its own module (not listed in the repo root go.work); disable the workspace
# so `go` uses e2e/linstor-migrator/go.mod instead of failing with "directory prefix . does not
# contain modules listed in go.work".
export GOWORK := off

.PHONY: help
help: ## Show help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: ## Download dependencies
	go mod download
	go mod tidy

.PHONY: test-scenario
test-scenario: ## Run a specific scenario by label (make test-scenario LABEL="Manual")
	@if [ -z "$(LABEL)" ]; then \
		echo "Error: specify LABEL=<scenario label> (Manual, WithoutPV)"; \
		exit 1; \
	fi
	go test -v -count=1 -timeout 60m ./tests/ -run "TestLinstorMigrator" -ginkgo.label-filter="$(LABEL)"

.PHONY: test-scenario-it
test-scenario-it: ## Run a focused It block within a scenario (make test-scenario-it LABEL="Manual" FOCUS="RV resource checks")
	@if [ -z "$(LABEL)" ]; then \
		echo "Error: specify LABEL=<scenario label> (Manual, WithoutPV)"; \
		exit 1; \
	fi
	@if [ -z "$(FOCUS)" ]; then \
		echo "Error: test-scenario-it needs FOCUS=<ginkgo focus regex> for one It/spec."; \
		echo "To run the whole scenario by label only, use: make test-scenario LABEL=\"$(LABEL)\""; \
		exit 1; \
	fi
	go test -v -count=1 -timeout 60m ./tests/ -run "TestLinstorMigrator" -ginkgo.label-filter="$(LABEL)" -ginkgo.focus="$(FOCUS)"

.PHONY: clean
clean: ## Clean test cache
	go clean -testcache

.PHONY: check-env
check-env: ## Print relevant env vars
	@echo "DKP_LICENSE_KEY:                            masked ($$(LC_ALL=C printf '%s' "$${DKP_LICENSE_KEY:-}" | wc -c | awk '{print $$1}') chars)"
	@echo "KUBE_CONFIG_PATH:                           $${KUBE_CONFIG_PATH}"
	@echo "REGISTRY_DOCKER_CFG:                        masked ($$(LC_ALL=C printf '%s' "$${REGISTRY_DOCKER_CFG:-}" | wc -c | awk '{print $$1}') chars)"
	@echo "SSH_HOST:                                   $${SSH_HOST}"
	@echo "SSH_JUMP_HOST:                              $${SSH_JUMP_HOST}"
	@echo "SSH_JUMP_USER:                              $${SSH_JUMP_USER}"
	@echo "SSH_PASSPHRASE:                             masked ($$(LC_ALL=C printf '%s' "$${SSH_PASSPHRASE:-}" | wc -c | awk '{print $$1}') chars)"
	@echo "SSH_PRIVATE_KEY:                            masked ($$(LC_ALL=C printf '%s' "$${SSH_PRIVATE_KEY:-}" | wc -c | awk '{print $$1}') chars)"
	@echo "SSH_PUBLIC_KEY:                             masked ($$(LC_ALL=C printf '%s' "$${SSH_PUBLIC_KEY:-}" | wc -c | awk '{print $$1}') chars)"
	@echo "SSH_USER:                                   $${SSH_USER}"
	@echo "TEST_CLUSTER_CLEANUP:                       $${TEST_CLUSTER_CLEANUP}"
	@echo "TEST_CLUSTER_CREATE_MODE:                   $${TEST_CLUSTER_CREATE_MODE}"
	@echo "TEST_CLUSTER_FORCE_LOCK_RELEASE:            $${TEST_CLUSTER_FORCE_LOCK_RELEASE}"
	@echo "TEST_CLUSTER_NAMESPACE:                     $${TEST_CLUSTER_NAMESPACE}"
	@echo "TEST_CLUSTER_STORAGE_CLASS:                 $${TEST_CLUSTER_STORAGE_CLASS}"
	@echo "TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME:    $${TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME}"
	@echo "TEST_DEBUG:                                 $${TEST_DEBUG}"
	@echo "TEST_MIGRATOR_MPO_IMAGE_TAG:                $${TEST_MIGRATOR_MPO_IMAGE_TAG}"
	@echo "TEST_PREVIOUS_RUNID:                        $${TEST_PREVIOUS_RUNID}"

.DEFAULT_GOAL := help
