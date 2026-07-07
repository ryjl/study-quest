.PHONY: build-admin build run run-admin test test-admin docker-build docker-run clean migrate build-apk build-apk-arm64 build-apk-arm build-apk-x64 build-apk-fat

# Build the admin SPA (React/Vite). Output lands in
# backend/internal/admin/spa/dist and is embedded into the Go binary via go:embed.
build-admin:
	@echo "==> Building Admin SPA (React/Vite)..."
	@cd frontend-admin && npm ci && npm run build

# 编译 Go 后端（含已嵌入的 Admin SPA）。先确保前端已构建。
build: build-admin
	@echo "==> Building Go Backend..."
	@cd backend && go build -o bin/server cmd/server/main.go

# 运行 Go 后端开发服务器。先重建 Admin SPA，因为 SPA 是通过 go:embed
# 静态嵌入进二进制的 —— 不重建 dist/ 的话 go run 跑的还是旧前端。
run: build-admin
	@echo "==> Running Go Backend (with freshly built SPA)..."
	@cd backend && go run cmd/server/main.go

# 仅前端热更新开发：vite dev server 直接代理到后端 :8080，改前端源码即时生效，
# 不必每次重建嵌入的 dist/。需要后端单独开着（make run 或 go run）。
run-admin:
	@echo "==> Vite dev server (proxy → :8080). Start backend separately."
	@cd frontend-admin && npm run dev

# 运行 Go 测试
test:
	@echo "==> Testing Go Backend..."
	@cd backend && go test -v ./...

# 运行前端测试
test-admin:
	@echo "==> Testing Admin SPA..."
	@cd frontend-admin && npm test

# 构建 Docker 镜像（多阶段：Node 构建 SPA → Go 构建二进制 → Alpine 运行时）
# 注意：build context 是仓库根，这样 SPA 阶段才能访问 frontend-admin/ 源码。
docker-build:
	@echo "==> Building Docker Image..."
	@docker build -f backend/Dockerfile -t studyquest-backend:latest .

# 运行 Docker 容器
docker-run:
	@echo "==> Running Docker Container..."
	@docker run -p 8080:8080 -e DB_PATH=/data/studyquest.db -v $(PWD)/data:/data studyquest-backend:latest

# ─── Flutter (Android APK) ────────────────────────────────────────────────
# FLUTTER 默认指向 ~/flutter/bin/flutter，可用环境变量覆盖：
#   make build-apk FLUTTER=/path/to/flutter/bin/flutter
FLUTTER ?= $(HOME)/flutter/bin/flutter

# 构建全部 ABI 的 release apk（推荐）。产物在
# frontend/build/app/outputs/flutter-apk/app-<abi>-release.apk。
#   arm64-v8a    → 主流 64 位 ARM 真机（绝大多数现代手机）
#   armeabi-v7a  → 老 32 位 ARM 设备
#   x86_64       → 模拟器
build-apk:
	@echo "==> Building Flutter release APKs (split per ABI)..."
	@cd frontend && $(FLUTTER) build apk --split-per-abi --release
	@echo "✓ APKs at frontend/build/app/outputs/flutter-apk/"

# 单 ABI 便捷目标。
build-apk-arm64:
	@cd frontend && $(FLUTTER) build apk --target-platform android-arm64 --release

build-apk-arm:
	@cd frontend && $(FLUTTER) build apk --target-platform android-arm --release

build-apk-x64:
	@cd frontend && $(FLUTTER) build apk --target-platform android-x64 --release

# 一个 fat APK（含所有 ABI，包体较大但安装最省心）。
build-apk-fat:
	@cd frontend && $(FLUTTER) build apk --release

# 清理构建产物
clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf backend/bin/
	@rm -rf backend/data/
	@rm -rf backend/internal/admin/spa/dist/*
	@rm -rf frontend-admin/node_modules/
	@rm -rf frontend/build/
