# Stage 1: Build binary using golang alpine with CGO enabled (required for SQLite)
FROM golang:1.23-alpine AS builder

# Install build dependencies for CGO compilation
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and templates
COPY . .

# Build statically linked binary with optimization flags
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o server cmd/server/main.go

# Stage 2: Runtime image using minimal alpine environment
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary and template files from the build container
COPY --from=builder /app/server .
COPY --from=builder /app/internal/admin/templates ./internal/admin/templates

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
