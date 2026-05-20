# syntax=docker/dockerfile:1.7
# SoyaOS Comet image: video-base@0.1.0
# 反向倒逼：DD-011 SilentCut 要求 30s 视频 ≤ 5min；冷启动 ≤ 10s ⇒ 多 stage + 极小 final layer。
FROM node:22-bookworm-slim AS base
RUN apt-get update && apt-get install -y --no-install-recommends \
      ffmpeg chromium fonts-noto-cjk fonts-inter ca-certificates \
    && rm -rf /var/lib/apt/lists/*
ENV PUPPETEER_SKIP_DOWNLOAD=true \
    PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium
RUN npm install -g @remotion/cli@4.0.0
WORKDIR /workdir
USER 1000:1000
ENTRYPOINT ["sleep","infinity"]
