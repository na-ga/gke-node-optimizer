NAME := gke-node-optimizer

GO_ENV ?= CGO_ENABLED=0
GO_PKGS ?= $(shell go list ./... | grep -v /vendor/)

GO ?= go
GODEP ?= godep
GOLINT ?= golint
MOCKGEN ?= mockgen
DOCKER ?= docker
KUBECTL ?= kubectl

.PHONY: build
build: BUILD_DIR ?= ./build
build: BUILD_ENV ?= GOOS=linux GOARCH=amd64
build:
	$(BUILD_ENV) $(GO_ENV) $(GO) build -o $(BUILD_DIR)/$(NAME)

.PHONY: vet
vet:
	$(GO_ENV) $(GO) vet $(GO_PKGS)

.PHONY: lint
lint:
	for pkg in $(GO_PKGS); do $(GOLINT) $$pkg; done

.PHONY: test
test:
	$(GO_ENV) $(GO) test -v $(GO_PKGS)

.PHONY: docker-build
docker-build: DOCKER_TAG ?= latest
docker-build:
	$(DOCKER) build -t naaga/$(NAME):$(DOCKER_TAG) .

.PHONY: docker-push
docker-push: DOCKER_TAG ?= latest
docker-push: docker-build
	$(DOCKER) push naaga/$(NAME):$(DOCKER_TAG)

.PHONY: run-gke
run-gke: JOB_VERSION ?= $(shell date +"%Y%m%d%H%M%S")
run-gke: NAMESPACE ?= default
run-gke: cleanup-gke
	$(KUBECTL) -n $(NAMESPACE) create job $(NAME)-spot-$(JOB_VERSION) --from=cronjob/$(NAME)

.PHONY: cleanup-gke
cleanup-gke: NAMESPACE ?= default
cleanup-gke:
	$(KUBECTL) -n $(NAMESPACE) delete job -l name=$(NAME)

.PHONY: mockgen
mockgen:
	$(MOCKGEN) -source ./gke/gke.go -destination ./mock/gke/gke.go
	$(MOCKGEN) -source ./report/report.go -destination ./mock/report/report.go
