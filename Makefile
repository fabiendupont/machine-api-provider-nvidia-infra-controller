# Image URL to use all building/pushing image targets
VERSION ?= 0.1.0
IMAGE_TAG_BASE ?= ghcr.io/fabiendupont/machine-api-provider-nico
IMG ?= $(IMAGE_TAG_BASE):latest
BUNDLE_IMG ?= $(IMAGE_TAG_BASE)-bundle:v$(VERSION)
CATALOG_IMG ?= $(IMAGE_TAG_BASE)-catalog:v$(VERSION)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

SETUP_ENVTEST ?= $(GOBIN)/setup-envtest
ENVTEST_K8S_VERSION ?= 1.36.2

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: ## Run unit tests.
	go test ./pkg/... -coverprofile cover.out

.PHONY: test-integration
test-integration: envtest ## Run integration tests with envtest.
	KUBEBUILDER_ASSETS="$(shell $(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --print path)" \
		go test ./test/integration/ -v

.PHONY: test-all
test-all: test test-integration ## Run all tests (unit + integration).

.PHONY: test-e2e-live
test-e2e-live: ## Run e2e tests against live NICo API.
	kind get kubeconfig --name nico-rest-local > /tmp/carbide-e2e-kubeconfig
	KUBECONFIG=/tmp/carbide-e2e-kubeconfig kubectl apply -f config/crd/external/
	KUBECONFIG=/tmp/carbide-e2e-kubeconfig \
		go test -tags=e2e ./test/e2e/ -v -ginkgo.v -ginkgo.label-filter="live"

##@ Build

.PHONY: build
build: fmt vet ## Build manager binary.
	go build -o bin/manager cmd/manager/main.go

.PHONY: run
run: fmt vet ## Run a controller from your host.
	go run ./cmd/manager/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} -f Dockerfile ../..

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

.PHONY: install
install: ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@echo "Note: Machine API types are provided by OpenShift"

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	@echo "Note: Machine API types are provided by OpenShift"

.PHONY: deploy
deploy: ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	kubectl apply -f config/rbac/
	kubectl apply -f config/manager/

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f config/manager/ --ignore-not-found=true
	kubectl delete -f config/rbac/ --ignore-not-found=true

##@ OLM Bundle

.PHONY: bundle-build
bundle-build: ## Build the OLM bundle image.
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push: ## Push the OLM bundle image.
	docker push $(BUNDLE_IMG)

##@ FBC Catalog

.PHONY: catalog-build
catalog-build: ## Build the FBC catalog image.
	docker build -f catalog.Dockerfile -t $(CATALOG_IMG) .

.PHONY: catalog-push
catalog-push: ## Push the FBC catalog image.
	docker push $(CATALOG_IMG)

##@ Tools

.PHONY: envtest
envtest: $(SETUP_ENVTEST) ## Install setup-envtest if needed.
$(SETUP_ENVTEST):
	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
