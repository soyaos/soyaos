# video-base (Comet image)

> [!WARNING]
> **SoyaOS is under active development and has not been formally released.**
> This image is unstable, unsigned, and may introduce breaking changes at any
> time. Use it only for isolated development and acceptance testing.

> Placeholder location. The canonical home for SoyaOS Comet images will be
> the dedicated `soyaos/comet-images` repository (created alongside the
> APP-517 build pipeline). The Dockerfile and manifest here are kept in the
> main `soyaos/soyaos` tree only until that split lands so the rest of
> Stage 5 (APP-508, APP-507) has a manifest target it can wire to without
> waiting on infra.

## What this image is

`video-base@0.1.0` is the Comet base image SilentCut (DD-011) provisions
when it needs to render an MP4 artifact. It bundles:

- Node 22 (bookworm-slim)
- Chromium (headless rendering for Remotion)
- ffmpeg (encode / mux)
- Remotion CLI 4.0.509 (exactly pinned)
- Noto CJK + Inter fonts (Chinese-and-English text in title cards)

Cold-start target: **≤ 10s** (DD-011 §SLA). Total size target: **≤ 800 MB**.

## Build (manual, until APP-517)

```sh
docker build \
  --platform=linux/amd64 \
  -t ghcr.io/soyaos/comet-video-base:0.1.0 \
  deploy/comet-images/video-base
```

## Push (manual, until APP-517)

```sh
docker push ghcr.io/soyaos/comet-video-base:0.1.0
```

APP-517 will add cosign signing, multi-arch (amd64 + arm64), and
automated nightly rebuilds. Until then this image is **unsigned** —
treat it as developer-mode only.

## Manifest (`image.yaml`)

The sibling `image.yaml` is the SoyaOS image-registry metadata. The
scheduler reads it to know resource floors / ceilings and the cold-start
target; the value `name@version` (here: `video-base@0.1.0`) is what
`runtime.Task.Image` references.
