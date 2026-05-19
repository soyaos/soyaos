#!/usr/bin/env bash
# scripts/test.sh — one-shot local CI runner
#
# Mirrors what .github/workflows/ci.yml does, so you can catch regressions
# locally before pushing.

set -euo pipefail

cd "$(dirname "$0")/.."

echo ">> go mod tidy"
go mod tidy

echo ">> gofmt check"
fmt_out=$(gofmt -l -s .)
if [ -n "$fmt_out" ]; then
  echo "gofmt issues:"
  echo "$fmt_out"
  echo "Run: gofmt -w -s ."
  exit 1
fi

echo ">> go vet"
go vet ./...

echo ">> go test (race)"
go test -race -count=1 ./...

echo ">> go build"
mkdir -p bin
go build -trimpath -o bin/soyaos ./cmd/soyaos

echo
echo "OK — bin/soyaos built"
./bin/soyaos version || true
