SHELL :=/bin/bash

all: build
.PHONY: all

build:
	go build -o hub-cluster-controller cmd/main.go


