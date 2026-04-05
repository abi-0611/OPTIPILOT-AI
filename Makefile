# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Registry / repository for published images
REGISTRY     ?= ghcr.io/optipilot-ai/optipilot
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
IMG_MANAGER  ?= $(REGISTRY)/manager:$(VERSION)
IMG_HUB      ?= $(REGISTRY)/hub:$(VERSION)
IMG_ML       ?= $(REGISTRY)/ml:$(VERSION)

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to use
ENVTEST_K8S_VERSION = 1.31.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash functions to be used in recipes
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role "crd:allowDangerousTypes=true" webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject methods.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-path $(LOCALBIN)/k8s -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter.
	$(GOLANGCI_LINT) run ./...

##@ Build

.PHONY: ui
ui: ## Build the React dashboard (outputs to ui/dashboard/dist/).
	cd ui/dashboard && npm ci && npm run build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary (without embedded UI).
	go build -o bin/manager ./cmd/manager

.PHONY: build-with-ui
build-with-ui: ui manifests generate fmt vet ## Build manager binary with embedded React dashboard.
	go build -tags ui -o bin/manager ./cmd/manager

.PHONY: build-hub
build-hub: manifests generate fmt vet ## Build hub controller binary (multi-cluster orchestrator).
	go build -o bin/hub ./cmd/hub

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/manager/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Image Builds (ko + Docker)

KO ?= $(LOCALBIN)/ko
KO_VERSION ?= v0.17.1

.PHONY: ko
ko: $(KO) ## Download ko locally if necessary.
$(KO): $(LOCALBIN)
	$(call go-install-tool,$(KO),github.com/google/ko,$(KO_VERSION))

.PHONY: image-manager
image-manager: ko ## Build manager image with ko (distroless, multi-arch).
	KO_DOCKER_REPO=$(REGISTRY) $(KO) build ./cmd/manager --bare --tags $(VERSION),latest \
		--platform linux/amd64,linux/arm64

.PHONY: image-hub
image-hub: ko ## Build hub image with ko (distroless, multi-arch).
	KO_DOCKER_REPO=$(REGISTRY) $(KO) build ./cmd/hub --bare --tags $(VERSION),latest \
		--platform linux/amd64,linux/arm64

.PHONY: image-ml
image-ml: ## Build ML service Docker image (multi-stage Python).
	docker build -t $(IMG_ML) ml/

.PHONY: images
images: image-manager image-hub image-ml ## Build all three container images.

.PHONY: push-manager
push-manager: image-manager ## Build and push manager image.
	@echo "Manager image pushed to $(REGISTRY)/manager:$(VERSION)"

.PHONY: push-hub
push-hub: image-hub ## Build and push hub image.
	@echo "Hub image pushed to $(REGISTRY)/hub:$(VERSION)"

.PHONY: push-ml
push-ml: image-ml ## Build and push ML service image.
	docker push $(IMG_ML)

.PHONY: push
push: push-manager push-hub push-ml ## Build and push all images to $(REGISTRY).
	@echo "All images pushed. Version: $(VERSION)"

.PHONY: image-load-kind
image-load-kind: ## Build images and load into the local kind cluster.
	docker build -t $(IMG_MANAGER) -f Dockerfile .
	docker build -t $(IMG_HUB) -f Dockerfile.hub .
	kind load docker-image $(IMG_MANAGER) --name $(KIND_CLUSTER_NAME)
	kind load docker-image $(IMG_HUB) --name $(KIND_CLUSTER_NAME)

##@ Deployment

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=true -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster.
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: manifests kustomize ## Undeploy controller from the K8s cluster.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=true -f -

##@ kind targets

KIND_CLUSTER_NAME ?= optipilot-dev

.PHONY: kind-create
kind-create: ## Create a local kind cluster.
	kind create cluster --name $(KIND_CLUSTER_NAME) --config hack/kind-config.yaml

.PHONY: kind-destroy
kind-destroy: ## Delete the local kind cluster.
	kind delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY: kind-deploy
kind-deploy: docker-build ## Load image into kind and deploy.
	kind load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)
	$(MAKE) install
	$(MAKE) deploy

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests.
	go test ./test/e2e/ -v -count=1

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.4.3
CONTROLLER_TOOLS_VERSION ?= v0.16.4
ENVTEST_VERSION ?= release-0.19
GOLANGCI_LINT_VERSION ?= v1.61.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom tooling specification
define go-install-tool
@[ -f "$(1)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
}
endef
