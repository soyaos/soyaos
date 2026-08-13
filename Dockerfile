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

# Cache module downloads in their own layer.
COPY go.mod go.sum ./
RUN go mod download

# Bring in the rest of the source tree.
COPY . .

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

# 7474 = control/HTTP, 7475 = gRPC, 7443/udp = optional ciphertext relay,
# 7480 = optional relay health endpoint. See cmd/soyaos for flag semantics.
EXPOSE 7474 7475 7443/udp 7480

ENTRYPOINT ["/soyaos"]
CMD ["start", "--listen", "0.0.0.0:7474", "--rpc", "0.0.0.0:7475", "--data-dir", "/data"]
