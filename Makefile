SHELL := /bin/bash

.DEFAULT_GOAL := default

.PHONY: default build go-build

default: build

build: go-build
	@echo "Build completed."

go-build:
	go build -ldflags='-s -w -linkmode external -extldflags "-static"' -o xportal ./cmd/xportal