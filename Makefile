# Makefile for CloudX

VERSION ?= 0.1.0-dev
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "unknown")
PKG_VERSION := github.com/cloudx-org/cloudx/internal/common/version

LDFLAGS := -X '$(PKG_VERSION).Version=$(VERSION)' \
           -X '$(PKG_VERSION).GitCommit=$(GIT_COMMIT)' \
           -X '$(PKG_VERSION).BuildDate=$(BUILD_DATE)'

.PHONY: all build test clean run-cloudx run-worker

all: build test

build:
	go build -ldflags="$(LDFLAGS)" -o bin/cloudx ./cmd/cloudx
	go build -ldflags="$(LDFLAGS)" -o bin/cloudx-worker ./cmd/cloudx-worker

test:
	go test -v ./...

clean:
	rm -rf bin/
