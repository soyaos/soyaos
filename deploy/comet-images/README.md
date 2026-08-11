# SoyaOS Comet base images

This directory holds the Dockerfile + `image.yaml` manifest for each
pre-warmed Comet base image. Comet images are referenced from
`runtime.Task.Image` as `name@version` (e.g. `video-base@0.1.0`).

> The canonical home for these images will be the dedicated
> `soyaos/comet-images` repository once APP-517 (build pipeline +
> cosign signing) ships. They live here today to keep Stage 5 work
> unblocked.

## Catalog

| Image            | Status      | Purpose                                                       |
|------------------|-------------|---------------------------------------------------------------|
| `video-base`     | live (alpha)| SilentCut (DD-011) — Node 22 + Chromium + ffmpeg + Remotion. |
| `text-only`      | planned     | Cheap Markdown / HTML / PDF jobs — no Chromium needed.        |
| `html-base`      | planned     | HTML + headless Chrome but no ffmpeg/Remotion.                |

The `runtime.BuiltinImages()` helper in `pkg/runtime/images.go` is the
Go-side index of what's available; keep it in sync when adding entries
to this table.

## Per-image layout

```
deploy/comet-images/<name>/
  Dockerfile      # multi-stage build, slim base
  image.yaml      # SoyaOS registry manifest (spec_version: comet-image.v0)
  README.md       # build / push notes
```

## Conventions

- Always pin the base image tag (no `latest`).
- Always run as a non-root UID (`USER 1000:1000`).
- Strip apt caches in the same RUN layer to keep size down.
- `cold_start_target_ms` in `image.yaml` is the contract the scheduler
  relies on for SilentCut's per-second billing math; revise it only
  with the matching benchmark in `bench/` (TODO once APP-517 lands).
