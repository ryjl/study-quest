# Build context for this Dockerfile is the REPOSITORY ROOT (not backend/),
# so that the frontend-admin/ sources are accessible to the SPA build stage.
# Build with:  docker build -f backend/Dockerfile -t studyquest-backend .   (from repo root)
# The Makefile docker-build target already does this.

# Stage 0: Build the admin SPA (React + Vite + TypeScript).
# Output: /build/backend/internal/admin/spa/dist — mirrors the embed path
# inside the repo so the next stage can COPY it verbatim.
FROM node:20-alpine AS spa
WORKDIR /build
COPY frontend-admin/package.json frontend-admin/package-lock.json ./
RUN npm ci
COPY frontend-admin/ ./
# vite.config.ts sets outDir to ../backend/internal/admin/spa/dist (relative
# to frontend-admin/). Create that target dir so the build can write into it.
RUN mkdir -p /build/backend/internal/admin/spa/dist && npm run build

# Stage 1: Build the Go binary with CGO enabled (required for SQLite).
# The SPA dist from stage 0 is placed at internal/admin/spa/dist so go:embed
# bakes it into the single resulting binary.
FROM golang:1.23-alpine AS builder

# Install build dependencies for CGO compilation
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy dependency manifests
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy backend source code
COPY backend/ ./

# Drop the SPA build output into the embed path, then compile.
COPY --from=spa /build/backend/internal/admin/spa/dist ./internal/admin/spa/dist

# Build statically linked binary with optimization flags
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o server cmd/server/main.go

# Stage 2: Runtime image using minimal alpine environment
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the self-contained binary (templates no longer needed — SPA is embedded)
COPY --from=builder /app/server .

# Create volume folders for persistent SQLite and Subtitles storage
RUN mkdir -p /app/data/subtitles

# Expose server listener port
EXPOSE 8080

# Environment variables
ENV SERVER_ADDR=0.0.0.0:8080
ENV DB_PATH=/app/data/studyquest.db

# Run as non-root user for security compliance
RUN adduser -D -u 10001 appuser && chown -R appuser:appuser /app
USER appuser

CMD ["./server"]
