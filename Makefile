# SoyaOS — top-level Makefile
#
# Usage:
#   make build     build the soyaos binary into ./bin/
#   make test      run unit tests
#   make vet       run go vet across every workspace module
#   make fmt       gofmt -w on everything
#   make fmt-check verify formatting without changing files
#   make tidy      tidy every module independently (GOWORK=off)
#   make lint      run golangci-lint if available
#   make all       fmt-check + vet + test + build
#   make clean     remove build artifacts

BIN_DIR        := bin
BIN            := $(BIN_DIR)/soyaos
MODULE_DIRS     := $(shell go list -m -f '{{.Dir}}')
MODULE_PACKAGES := $(addsuffix /...,$(MODULE_DIRS))
GIT_SHA        := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Version sourced from the latest git tag — tags are the source of truth.
# Falls back to 0.0.0 when no tag exists yet.
VERSION        := $(shell git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || echo 0.0.0)
LDFLAGS        := -X github.com/soyaos/soyaos/pkg/version.GitSHA=$(GIT_SHA) -X github.com/soyaos/soyaos/pkg/version.Version=$(VERSION)

.PHONY: all
all: fmt-check vet test build

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/soyaos

.PHONY: test
test:
	go test -race -count=1 $(MODULE_PACKAGES)

.PHONY: test-independent
test-independent:
	@set -e; for module_dir in $(MODULE_DIRS); do \
		echo "==> GOWORK=off go test $${module_dir}"; \
		(cd "$${module_dir}" && GOWORK=off go test -count=1 ./...); \
	done

.PHONY: vet
vet:
	go vet $(MODULE_PACKAGES)

.PHONY: fmt
fmt:
	gofmt -w -s cmd pkg

.PHONY: fmt-check
fmt-check:
	@fmt_out="$$(gofmt -l -s cmd pkg)"; \
	if [ -n "$${fmt_out}" ]; then \
		echo "gofmt issues in:"; \
		echo "$${fmt_out}"; \
		exit 1; \
	fi

.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		set -e; for module_dir in $(MODULE_DIRS); do \
			echo "==> golangci-lint $${module_dir}"; \
			(cd "$${module_dir}" && golangci-lint run ./...); \
		done; \
	else \
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run)"; \
	fi

.PHONY: tidy
tidy:
	@set -e; for module_dir in $(MODULE_DIRS); do \
		echo "==> GOWORK=off go mod tidy $${module_dir}"; \
		(cd "$${module_dir}" && GOWORK=off go mod tidy); \
	done

.PHONY: tidy-check
tidy-check: tidy
	git diff --exit-code -- ':(glob)**/go.mod' ':(glob)**/go.sum' go.work go.work.sum

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
	go clean -cache -testcache 2>/dev/null || true

.PHONY: test-module-release
test-module-release:
	python3 -m unittest scripts/test_module_release.py

.PHONY: ci
ci: tidy-check fmt-check vet test test-independent test-module-release build
