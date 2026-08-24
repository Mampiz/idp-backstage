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
STATUS_API_IMAGE     ?= idp/status-api:dev
SCAFFOLDER_IMAGE     ?= idp/scaffolder:dev
TECHDOCS_IMAGE       ?= spotify/techdocs
CERT_MANAGER_URL     ?= https://github.com/cert-manager/cert-manager/releases/download/$(CERT_MANAGER_VERSION)/cert-manager.yaml

KUBECTL := kubectl --context=$(KUBE_CONTEXT)
GO_SERVICES := services/status-api services/scaffolder

# Local configuration and secrets live in .env (gitignored, see .env.example).
# Loaded here and exported so Backstage and the Go services pick them up.
-include .env
export
GITHUB_OWNER  ?= Mampiz
POSTGRES_HOST ?= localhost
POSTGRES_PORT ?= 5432
POSTGRES_USER ?= backstage
POSTGRES_PASSWORD ?= backstage
POSTGRES_DB ?= backstage

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
	@# Available is not the same as serving: see the comment in the script.
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/wait-cert-manager.sh

.PHONY: operator-install
operator-install: ## Install webapp-operator $(OPERATOR_VERSION) from its dist/install.yaml
	$(KUBECTL) apply -f $(OPERATOR_INSTALL_URL)
	$(KUBECTL) -n webapp-operator-system wait deployment/webapp-operator-controller-manager \
		--for=condition=Available --timeout=300s
	@# Upstream gap: the operator emits events but its ClusterRole has no rule for
	@# them, so every event is rejected. See infra/operator/events-rbac.yaml.
	$(KUBECTL) apply -f infra/operator/events-rbac.yaml
	@# Available is not the same as serving, and the webhooks fail closed.
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/wait-operator-webhook.sh

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
	@# GOWORK=off on purpose: inside go.work the workspace supplies the build
	@# list, so go.sum can stay incomplete and only fail later in a container
	@# build, where there is no workspace.
	@for s in $(GO_SERVICES); do (cd $$s && GOWORK=off go mod tidy); done

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

##@ F1 - Backstage

.PHONY: db-up
db-up: ## Start the Postgres backing Backstage
	docker compose up -d postgres
	@until docker compose exec -T postgres pg_isready -U $(POSTGRES_USER) -d $(POSTGRES_DB) >/dev/null 2>&1; do sleep 1; done
	@echo "postgres ready on $(POSTGRES_HOST):$(POSTGRES_PORT)"

.PHONY: db-down
db-down: ## Stop Postgres (keeps the data volume)
	docker compose stop postgres

.PHONY: db-nuke
db-nuke: ## Stop Postgres AND delete its data volume
	docker compose down -v

.PHONY: backstage-install
backstage-install: ## Install Backstage dependencies
	cd backstage && yarn install --immutable

.PHONY: require-github-token
require-github-token:
	@[ -n "$$GITHUB_TOKEN" ] || { \
		echo "GITHUB_TOKEN is not exported."; \
		echo "It is deliberately not kept in .env. Run:"; \
		echo "  export GITHUB_TOKEN=\$$(gh auth token)"; \
		exit 1; }

.PHONY: services-up
services-up: status-api-deploy scaffolder-deploy ## Deploy both Go services into the cluster

.PHONY: dev
dev: require-github-token db-up services-up ## Run the whole platform locally
	cd backstage && yarn start

.PHONY: verify-f1
verify-f1: require-github-token ## F1 verifier: Backstage boots on Postgres, serves the catalog, and survives a DB restart
	@./infra/scripts/verify-f1.sh

##@ F2 - Status API

.PHONY: status-api-run
status-api-run: ## Run the status API locally against the kind cluster
	KUBE_CONTEXT=$(KUBE_CONTEXT) go run ./services/status-api/cmd/status-api

.PHONY: status-api-image
status-api-image: ## Build the status API image and load it into kind
	docker build -t $(STATUS_API_IMAGE) ./services/status-api
	kind load docker-image $(STATUS_API_IMAGE) --name $(CLUSTER_NAME)

.PHONY: status-api-deploy
status-api-deploy: status-api-image ## Deploy the status API into the cluster
	$(KUBECTL) apply -f infra/k8s/status-api/manifests.yaml
	$(KUBECTL) -n idp-system rollout status deployment/status-api --timeout=180s
	@echo "status-api available on http://localhost:30081"

.PHONY: verify-f2
verify-f2: ## F2 verifier: unit tests, plus the API serving the real cluster contents
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/verify-f2.sh

##@ F3 - Scaffolder

.PHONY: scaffolder-run
scaffolder-run: require-github-token ## Run the scaffolder locally against the kind cluster
	KUBE_CONTEXT=$(KUBE_CONTEXT) GITHUB_OWNER=$(GITHUB_OWNER) go run ./services/scaffolder/cmd/scaffolder

.PHONY: scaffolder-image
scaffolder-image: ## Build the scaffolder image and load it into kind
	docker build -t $(SCAFFOLDER_IMAGE) ./services/scaffolder
	kind load docker-image $(SCAFFOLDER_IMAGE) --name $(CLUSTER_NAME)

.PHONY: scaffolder-secret
scaffolder-secret: require-github-token ## Create the GitHub token secret from the environment
	@# Piped through apply so the token never lands in a file on disk.
	@$(KUBECTL) create namespace idp-system --dry-run=client -o yaml | $(KUBECTL) apply -f - >/dev/null
	@$(KUBECTL) create secret generic scaffolder-github \
		--namespace idp-system \
		--from-literal=token="$$GITHUB_TOKEN" \
		--dry-run=client -o yaml | $(KUBECTL) apply -f - >/dev/null
	@echo "secret idp-system/scaffolder-github updated from \$$GITHUB_TOKEN"

.PHONY: scaffolder-deploy
scaffolder-deploy: scaffolder-image scaffolder-secret ## Deploy the scaffolder into the cluster
	$(KUBECTL) apply -f infra/k8s/scaffolder/manifests.yaml
	$(KUBECTL) -n idp-system rollout restart deployment/scaffolder
	$(KUBECTL) -n idp-system rollout status deployment/scaffolder --timeout=180s
	@echo "scaffolder available on http://localhost:30080"

.PHONY: verify-f3
verify-f3: require-github-token ## F3 verifier: a real repository, a real custom resource, real pods
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/verify-f3.sh

##@ F4 - Software template

.PHONY: verify-f4
verify-f4: require-github-token ## F4 verifier: executing the template produces repo + custom resource + pods
	@KUBE_CONTEXT=$(KUBE_CONTEXT) GITHUB_OWNER=$(GITHUB_OWNER) ./infra/scripts/verify-f4.sh

##@ F5 - Frontend plugin

.PHONY: verify-f5
verify-f5: ## F5 verifier: the WebApp tab shows the real cluster state and follows a kubectl scale
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/verify-f5.sh

##@ F6 - Documentation

.PHONY: docs-build
docs-build: ## Build the TechDocs site locally with the official image
	docker run --rm -v "$(CURDIR)":/content -w /content --entrypoint mkdocs \
		$(TECHDOCS_IMAGE) build -d /tmp/techdocs-site

.PHONY: record-demo
record-demo: require-github-token ## Re-record the README demo (creates a real repository)
	./infra/scripts/record-demo.sh

.PHONY: verify-f6
verify-f6: ## F6 verifier: the docs build, the portal serves them, and the demo exists
	@KUBE_CONTEXT=$(KUBE_CONTEXT) ./infra/scripts/verify-f6.sh

##@ Utils

.PHONY: kubectx
kubectx: ## Show which cluster the tooling talks to
	@echo "context: $(KUBE_CONTEXT)"
	@$(KUBECTL) config view --minify -o jsonpath='{.clusters[0].cluster.server}'; echo
