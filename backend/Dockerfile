# Build context for this Dockerfile is the REPOSITORY ROOT (not backend/),
# so that the frontend-admin/ sources are accessible to the SPA build stage.
# Build with:  docker build -f backend/Dockerfile -t studyquest-backend .   (from repo root)
# The Makefile docker-build target already does this.

# Stage -1: Fetch the local ONNX embedding artifacts (libonnxruntime + BGE model).
# These are baked into the image so a fresh `docker run` has working embeddings
# with no host-volume dependency. ~45MB total; layer caches well (only changes
# on an onnxruntime/BGE version bump). Version pins mirror Makefile's
# fetch-ai-models target — keep them in sync.
FROM alpine:3.19 AS ai-models
RUN apk add --no-cache curl tar
ARG ORT_VERSION=1.26.0
WORKDIR /models
# 1) onnxruntime C library (versioned .so — onnxruntime_go's ABI check needs the
#    exact versioned path, not the unversioned symlink). NOTE: this is the
#    official glibc build of onnxruntime — it requires glibc at RUNTIME, which
#    is why the runtime stage below is debian-slim (not alpine/musl). The
#    previous alpine runtime failed with "loading ld-linux-x86-64.so.2: No such
#    file" because musl lacks the glibc dynamic linker.
RUN curl -fL --retry 3 -o /tmp/ort.tgz \
      "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz" \
    && tar -xzf /tmp/ort.tgz -C /tmp \
    && cp /tmp/onnxruntime-linux-x64-${ORT_VERSION}/lib/libonnxruntime.so.${ORT_VERSION} \
          /models/libonnxruntime.so.${ORT_VERSION} \
    && rm -rf /tmp/*
# 2) BGE-small-zh int8 quantized model + vocab.
RUN mkdir -p /models/bge-small-zh-v1.5 \
    && curl -fL --retry 3 -o /models/bge-small-zh-v1.5/model_quantized.onnx \
       "https://huggingface.co/Xenova/bge-small-zh-v1.5/resolve/main/onnx/model_quantized.onnx" \
    && curl -fL --retry 3 -o /models/bge-small-zh-v1.5/vocab.txt \
       "https://huggingface.co/Xenova/bge-small-zh-v1.5/resolve/main/vocab.txt"

# Stage 0: Build the admin SPA (React + Vite + TypeScript).
# Output: /build/backend/internal/admin/spa/dist — mirrors the embed path
# inside the repo so the next stage can COPY it verbatim.
#
# WORKDIR must be /build/frontend-admin (NOT /build) so that vite.config.ts's
# outDir "../backend/internal/admin/spa/dist" resolves to /build/backend/...
# (matching the COPY --from=spa source path below). With WORKDIR=/build the
# "../" escapes /build and the build writes to /backend/... instead — which the
# next stage can't find, so it silently embeds whatever stale local dist/ the
# `COPY backend/ ./` step dragged in. This was the root cause of the
# "stale SPA / missing retry button after deploy" bug.
FROM node:20-alpine AS spa
WORKDIR /build/frontend-admin
COPY frontend-admin/package.json frontend-admin/package-lock.json ./
RUN npm ci
COPY frontend-admin/ ./
# vite.config.ts sets outDir to ../backend/internal/admin/spa/dist (relative
# to frontend-admin/, i.e. relative to this WORKDIR). Create that target dir so
# the build can write into it.
RUN mkdir -p /build/backend/internal/admin/spa/dist && npm run build

# Stage 1: Build the Go binary with CGO enabled (required for SQLite).
# The SPA dist from stage 0 is placed at internal/admin/spa/dist so go:embed
# bakes it into the single resulting binary.
#
# Uses the standard golang image (debian-based, NOT alpine) so CGO compiles
# against glibc — matching the debian-slim runtime below. Building against musl
# (alpine) then running on glibc (debian) would mismatch the SQLite/cgo ABI.
FROM golang:1.23-bookworm AS builder

# Install build dependencies for CGO compilation (gcc + glibc headers, required
# for go-sqlite3). We no longer UPX-compress the binary: upx isn't in the
# golang:1.23-bookworm default apt sources, and the size saving (~10-15MB) is
# marginal vs the image's real heavyweights (ffmpeg ~80MB + ONNX models ~45MB).
# Skipping it also keeps the binary debuggable (strace/gdb symbols intact).
RUN apt-get update && apt-get install -y --no-install-recommends \
        gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy dependency manifests
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy backend source code
COPY backend/ ./

# Drop the SPA build output into the embed path, then compile.
COPY --from=spa /build/backend/internal/admin/spa/dist ./internal/admin/spa/dist

# Build with CGO enabled (glibc, matching the debian-slim runtime). -ldflags
# -s -w strips debug info to shrink the binary without UPX.
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o server cmd/server/main.go

# Stage -2: Fetch static ffmpeg/ffprobe binaries (ffbinaries v6.1 linux-64).
# The Go code only invokes ffprobe (metadata), ffmpeg -vframes 1 (one-frame
# screenshot for cover) and ffmpeg -c copy (extract embedded cover) — all
# covered by the static GPL build, no transcoding. Replacing apt `ffmpeg`
# (which pulls ~470MB of libav* codec/font/GUI deps) with two static binaries
# (~58MB total) is the single biggest image win: 665MB → ~275MB.
# ffbinaries.com serves GitHub release zips; --retry 3 guards against the
# transient 404s observed in earlier docker build attempts.
FROM debian:bookworm-slim AS ffmpeg-static
RUN apt-get update && apt-get install -y --no-install-recommends curl unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp
ARG FFBIN_VER=6.1
RUN curl -fL --retry 3 -o ffmpeg.zip \
      "https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v${FFBIN_VER}/ffmpeg-${FFBIN_VER}-linux-64.zip" \
    && curl -fL --retry 3 -o ffprobe.zip \
      "https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v${FFBIN_VER}/ffprobe-${FFBIN_VER}-linux-64.zip" \
    && unzip -o ffmpeg.zip -d /out && unzip -o ffprobe.zip -d /out \
    && chmod +x /out/ffmpeg /out/ffprobe \
    && rm -f ffmpeg.zip ffprobe.zip

# Stage 2: Runtime image using debian-slim (glibc).
#
# MUST be glibc-based (not alpine/musl) because libonnxruntime.so from the
# ai-models stage is the official glibc build — it needs ld-linux-x86-64.so.2
# (the glibc dynamic linker), which musl lacks. The previous alpine runtime
# failed at embedding init with "loading ld-linux-x86-64.so.2: No such file".
# debian-slim is also a safer base for any future cgo/.so dependency.
#
# SIZE STATUS (2026-07, optimized): image is ~275MB. The previously dominant
# apt `ffmpeg` (~470MB) was replaced by two static binaries from the
# ffmpeg-static stage (~58MB combined). Remaining cost: debian-slim base
# (~75MB) + ONNX models (~45MB, baked-in) + Go binary (~14MB) + static
# ffmpeg/ffprobe (~58MB) + ca-certs/tzdata/curl (~15MB).
FROM debian:bookworm-slim

# ca-certificates for HTTPS to the chat relay / huggingface / github.
# tzdata for appclock business-timezone handling. curl for operator debugging.
# (ffmpeg/ffprobe are copied from the ffmpeg-static stage below — NOT apt, to
# avoid pulling ~470MB of libav codec deps for code that never transcodes.)
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates tzdata curl \
    && rm -rf /var/lib/apt/lists/*

# Static ffmpeg/ffprobe (ffbinaries v6.1). Used by episode_service.go for
# ffprobe metadata + ffmpeg one-frame/cover extraction. glibc statically
# linked into the binary, so no extra shared-lib deps in the runtime layer.
COPY --from=ffmpeg-static /out/ffmpeg /out/ffprobe /usr/local/bin/

WORKDIR /app

# Copy the self-contained binary (templates no longer needed — SPA is embedded)
COPY --from=builder /app/server .

# Bake the ONNX embedding artifacts into the image (from the ai-models stage).
# IMPORTANT: placed under /app/ai-models (NOT /app/data/ai-models), because
# `make deploy` bind-mounts the host volume over /app/data — anything under
# /app/data in the image gets SHADOWED by the (empty on first run) host dir, so
# the embedder would fail with "data 目录下没有". /app/ai-models is outside the
# mounted volume, so the baked-in files survive. `make deploy` sets
# AI_MODELS_DIR=/app/ai-models to match. ~45MB.
COPY --from=ai-models /models/ /app/ai-models/

# Create volume folders for persistent SQLite and Subtitles storage
RUN mkdir -p /app/data/subtitles

# Expose server listener port
EXPOSE 8080

# Environment variables
ENV SERVER_ADDR=0.0.0.0:8080
ENV DB_PATH=/app/data/studyquest.db

# Run as non-root user for security compliance (debian adduser syntax).
RUN useradd -u 10001 -r -s /sbin/nologin appuser && chown -R appuser:appuser /app
USER appuser

CMD ["./server"]
