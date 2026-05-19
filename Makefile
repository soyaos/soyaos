# SoyaOS — top-level Makefile
#
# Usage:
#   make build     build the soyaos binary into ./bin/
#   make test      run unit tests
#   make vet       run go vet
#   make fmt       gofmt -w on everything
#   make lint      run golangci-lint if available
#   make all       fmt + vet + test + build
#   make clean     remove build artifacts

BIN_DIR        := bin
BIN            := $(BIN_DIR)/soyaos
PKG            := ./...
GIT_SHA        := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Version sourced from the latest git tag — tags are the source of truth.
# Falls back to 0.0.0 when no tag exists yet.
VERSION        := $(shell git describe --tags --abbrev=0 2>/dev/null || echo 0.0.0)
LDFLAGS        := -X github.com/soyaos/soyaos/pkg/version.GitSHA=$(GIT_SHA) -X github.com/soyaos/soyaos/pkg/version.Version=$(VERSION)

.PHONY: all
all: fmt vet test build

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/soyaos

.PHONY: test
test:
	go test -race -count=1 $(PKG)

.PHONY: vet
vet:
	go vet $(PKG)

.PHONY: fmt
fmt:
	gofmt -w -s .

.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run)"; \
	fi

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
	go clean -cache -testcache 2>/dev/null || true

.PHONY: ci
ci: vet test build
