# Makefile for CloudX

VERSION ?= v1.0.0
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "unknown")
PKG_VERSION := github.com/cloudx-org/cloudx/internal/common/version

LDFLAGS := -s -w \
           -X '$(PKG_VERSION).Version=$(VERSION)' \
           -X '$(PKG_VERSION).GitCommit=$(GIT_COMMIT)' \
           -X '$(PKG_VERSION).BuildDate=$(BUILD_DATE)'

.PHONY: all build test test-integration test-race bench lint cross-build release clean help

all: lint build test

help:
	@echo "CloudX Build & Test Targets:"
	@echo "  make build             - Build CLI and Worker binaries for host platform"
	@echo "  make test              - Run all unit and subsystem tests"
	@echo "  make test-integration  - Run master integration and killer demo test suites"
	@echo "  make test-race         - Run concurrency and race condition tests"
	@echo "  make bench             - Run scheduler and reconciler scale benchmarks"
	@echo "  make lint              - Run go vet and gofmt check"
	@echo "  make cross-build       - Compile binaries for all supported platforms (Linux, macOS, Windows)"
	@echo "  make release           - Build and package versioned release archives with SHA256 checksums"
	@echo "  make clean             - Remove generated bin/ and release artifacts"

build:
	go build -ldflags="$(LDFLAGS)" -o bin/cloudx ./cmd/cloudx
	go build -ldflags="$(LDFLAGS)" -o bin/cloudx-worker ./cmd/cloudx-worker

test:
	go test -v ./...

test-integration:
	go test -v -run "TestFunctionalAudit|TestArchitectureAudit|TestReliabilityAudit|TestPhase87_KillerDemo|TestE2E_GoldenPathScenario" ./test/integration/...

test-race:
	go test -v -run TestRace_ ./test/integration/...

bench:
	go test -v -bench=. -benchmem -run=^$$ ./internal/scheduler/...
	go test -v -bench=. -benchmem -run=^$$ ./internal/controlplane/...

lint:
	go vet ./...
	gofmt -w .

cross-build:
	go run ./scripts/cross_build/main.go

release:
	CLOUDX_VERSION=$(VERSION) go run ./scripts/package_release/main.go

clean:
	rm -rf bin/
