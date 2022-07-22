# Copyright Contributors to the Open Cluster Management project

# -------------------------------------------------------------
# This makefile defines the following targets
#
#   - all (default) - format the code, run linters, download vendor libs, and build executable
#   - fmt - format the code
#   - lint - run code analysis tools
#   - vendor - download all third party libraries and put them inside vendor directory
#   - clean-vendor - remove third party libraries from vendor directory
#   - build - build the binary
#   - build-image - build docker image locally for running the components using docker
#   - push-image - push the local docker image to docker registry
#   - clean - clean the build directories
#   - clean-all - superset of 'clean' that also remove vendor dir

SHELL :=/bin/bash

COMPONENT := $(shell basename $(shell pwd))
REGISTRY ?= quay.io/open-cluster-management-hub-of-hubs
IMAGE_TAG ?= latest
IMAGE := ${REGISTRY}/${COMPONENT}:${IMAGE_TAG}

.PHONY: all				## format the code, run linters, downloads vendor libs, and build executable
all: vendor fmt lint build

.PHONY: fmt				## format the code
fmt:
	@go fmt ./cmd/... ./pkg/...
	@gofumpt -w ./cmd/ ./pkg/

.PHONY: lint		    ## run code analysis tools
lint:
	go vet ./cmd/... ./pkg/...
	golint ./cmd/... ./pkg/...
	golangci-lint run ./cmd/... ./pkg/...

.PHONY: vendor			## download all third party libraries and puts them inside vendor directory
vendor:
	@go mod vendor

.PHONY: clean-vendor    ## remove third party libraries from vendor directory
clean-vendor:
	-@rm -rf vendor

.PHONY: build			## build the binary
build:
	@go build -o bin/${COMPONENT} cmd/main.go

.PHONY: build-image	    ## build docker image locally for running the components using docker
build-image: vendor
	docker build -t ${IMAGE} --build-arg COMPONENT=${COMPONENT} -f Dockerfile .

.PHONY: push-image      ## push the local docker image to docker registry
push-image: build-image
	@docker push ${IMAGE}

.PHONY: clean			## clean the build directories
clean:
	@rm -rf bin

.PHONY: clean-all		## superset of 'clean' that also remove vendor dir
clean-all: clean-vendor clean

.PHONY: help			## show this help message
help:
	@echo "usage: make [target]\n"; echo "options:"; \fgrep -h "##" $(MAKEFILE_LIST) | fgrep -v fgrep | sed -e 's/\\$$//' | sed -e 's/##//' | sed 's/.PHONY:*//' | sed -e 's/^/  /'; echo "";
