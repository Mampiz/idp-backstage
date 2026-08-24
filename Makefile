# Root Makefile for the IDP monorepo.
# Everything targets the LOCAL kind cluster. Never a remote context: every
# kubectl invocation pins --context=$(KUBE_CONTEXT) explicitly.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# ---- Pinned versions (reproducibility over freshness) ----------------------
CLUSTER_NAME         ?= idp-local
KUBE_CONTEXT         ?= kind-$(CLUSTER_NAME)
KIND_CONFIG          ?= infra/kind/idp-local.yaml
CERT_MANAGER_VERSION ?= v1.21.1
OPERATOR_VERSION     ?= v1.0.0
OPERATOR_INSTALL_URL ?= https://raw.githubusercontent.com/Mampiz/webapp-operator/$(OPERATOR_VERSION)/dist/install.yaml
CERT_MANAGER_URL     ?= https://github.com/cert-manager/cert-manager/releases/download/$(CERT_MANAGER_VERSION)/cert-manager.yaml

KUBECTL := kubectl --context=$(KUBE_CONTEXT)
GO_SERVICES := services/status-api services/scaffolder

##@ General

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
	/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
	/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ F0 - Cluster & operator

.PHONY: cluster-up
cluster-up: ## Create the local kind cluster (idempotent)
	@if kind get clusters 2>/dev/null | grep -qx "$(CLUSTER_NAME)"; then \
		echo "kind cluster '$(CLUSTER_NAME)' already exists"; \
	else \
		kind create cluster --config $(KIND_CONFIG); \
	fi
	@$(KUBECTL) cluster-info >/dev/null

.PHONY: cluster-down
cluster-down: ## Delete the local kind cluster
	kind delete cluster --name $(CLUSTER_NAME)

.PHONY: cert-manager
cert-manager: ## Install cert-manager (REQUIRED: the operator's webhooks need CA injection)
	$(KUBECTL) apply -f $(CERT_MANAGER_URL)
	$(KUBECTL) -n cert-manager wait deployment --all --for=condition=Available --timeout=300s

.PHONY: operator-install
operator-install: ## Install webapp-operator $(OPERATOR_VERSION) from its dist/install.yaml
	$(KUBECTL) apply -f $(OPERATOR_INSTALL_URL)
	$(KUBECTL) -n webapp-operator-system wait deployment/webapp-operator-controller-manager \
		--for=condition=Available --timeout=300s
	@# Upstream gap: the operator emits events but its ClusterRole has no rule for
	@# them, so every event is rejected. See infra/operator/events-rbac.yaml.
	$(KUBECTL) apply -f infra/operator/events-rbac.yaml

.PHONY: bootstrap
bootstrap: cluster-up cert-manager operator-install ## Full F0 bring-up from zero

.PHONY: verify-f0
verify-f0: ## F0 verifier: CRD + operator + webhooks + a real WebApp reaching Ready
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/verify-f0.sh

.PHONY: clean-smoke
clean-smoke: ## Remove the F0 smoke-test resources
	-$(KUBECTL) delete -f infra/test/webapp-smoke.yaml --ignore-not-found
	-$(KUBECTL) delete namespace idp-demo --ignore-not-found

##@ Go

.PHONY: tidy
tidy: ## go mod tidy on every Go service
	@for s in $(GO_SERVICES); do (cd $$s && go mod tidy); done

.PHONY: build
build: ## Build every Go service into bin/
	@mkdir -p bin
	@for s in $(GO_SERVICES); do \
		(cd $$s && go build -o $(CURDIR)/bin/ ./...); \
	done
	@ls -1 bin/

.PHONY: test
test: ## Run Go tests across the workspace
	@for s in $(GO_SERVICES); do (cd $$s && go test ./...); done

.PHONY: fmt
fmt: ## gofmt every Go service
	@for s in $(GO_SERVICES); do (cd $$s && gofmt -l -w .); done

##@ Utils

.PHONY: kubectx
kubectx: ## Show which cluster the tooling talks to
	@echo "context: $(KUBE_CONTEXT)"
	@$(KUBECTL) config view --minify -o jsonpath='{.clusters[0].cluster.server}'; echo
