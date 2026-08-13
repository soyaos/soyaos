# syntax=docker/dockerfile:1.7
#
# soyaos — multi-stage container image.
#
# Stage 1 (builder): build a static, stripped soyaos binary using the official
# golang:1.23-alpine toolchain. CGO is disabled so the artifact is fully static
# and can run unmodified on a `scratch` base.
#
# Stage 2 (runtime): minimal `scratch` image. We carry over only the system CA
# bundle (so TLS to upstream LLM providers and registries works) and the
# soyaos binary itself. No shell, no package manager, no extra surface area.
#
# Build args:
#   VERSION  semantic version string baked into the binary (default: dev)
#   GITSHA   git commit SHA baked into the binary (default: unknown)
#   INDEPENDENT_MODULE_DOWNLOAD  resolve every module with GOWORK=off before
#                                building (default: true; false only while CI
#                                simulates not-yet-published module tags)
#
# Example:
#   docker build \
#     --build-arg VERSION=v0.1.0-alpha.0 \
#     --build-arg GITSHA=$(git rev-parse --short HEAD) \
#     -t ghcr.io/soyaos/soyaos:dev .

# --- builder ---
FROM golang:1.23-alpine AS builder

# git is required for `go build` to resolve VCS info; ca-certificates is the
# source we copy into the final image (Alpine path matches Debian path).
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Bring in the workspace and all module manifests. Each module is independently
# resolvable; the workspace only selects the local source during the build.
COPY . .
ARG INDEPENDENT_MODULE_DOWNLOAD=true
RUN set -eux; \
    if [ "${INDEPENDENT_MODULE_DOWNLOAD}" = "true" ]; then \
      for module_file in cmd/*/go.mod pkg/*/go.mod; do \
        module_dir="$(dirname "${module_file}")"; \
        (cd "${module_dir}" && GOWORK=off go mod download); \
      done; \
    else \
      echo "pending module tags: resolving dependencies through go.work"; \
    fi

ARG VERSION=dev
ARG GITSHA=unknown

# Static, trimmed, stripped build. -trimpath removes local filesystem paths
# from the binary; -s -w drops the symbol table and DWARF info.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X github.com/soyaos/soyaos/pkg/version.Version=${VERSION} -X github.com/soyaos/soyaos/pkg/version.GitSHA=${GITSHA}" \
    -o /out/soyaos ./cmd/soyaos

# --- runtime ---
FROM scratch

# TLS roots for outbound HTTPS (LLM providers, OCI registries, etc.).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/soyaos /soyaos

# 7474 = control/HTTP, 7475 = gRPC. See cmd/soyaos for flag semantics.
EXPOSE 7474 7475

ENTRYPOINT ["/soyaos"]
CMD ["start", "--listen", "0.0.0.0:7474", "--rpc", "0.0.0.0:7475", "--data-dir", "/data"]
