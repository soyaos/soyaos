#!/usr/bin/env bash
# scripts/test.sh — one-shot local CI runner
#
# Mirrors what .github/workflows/ci.yml does, so you can catch regressions
# locally before pushing.

set -euo pipefail

cd "$(dirname "$0")/.."

echo ">> module tidy check"
make tidy-check

echo ">> gofmt check"
make fmt-check

echo ">> go vet"
make vet

echo ">> go test (race)"
make test

echo ">> independent module tests (GOWORK=off)"
make test-independent

echo ">> go build"
mkdir -p bin
make build

echo
echo "OK — bin/soyaos built"
./bin/soyaos version || true
