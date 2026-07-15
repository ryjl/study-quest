.PHONY: build-admin build run run-admin test test-admin docker-build docker-run clean migrate build-apk build-apk-arm64 build-apk-arm build-apk-x64 build-apk-fat fetch-ai-models clean-ai-models

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

# ─── 本地 AI 模型 / ONNX 运行时 ───────────────────────────────────────────
# 下载本地 embedding 所需的 ONNX 运行时与 BGE-small-zh int8 量化模型，放到
# backend/data/ai-models/（该目录已被 .gitignore，二进制不进 git）。
#   libonnxruntime.so.1.26.0  —— onnxruntime 官方 release v1.26.0 (linux x64)
#   bge-small-zh-v1.5/...     —— Xenova/bge-small-zh-v1.5 的量化模型 + 词表
# 幂等：每个文件已存在且大小 > 0 则跳过。
AI_MODELS_DIR := backend/data/ai-models
ORT_VERSION   := 1.26.0
ORT_TGZ_URL   := https://github.com/microsoft/onnxruntime/releases/download/v$(ORT_VERSION)/onnxruntime-linux-x64-$(ORT_VERSION).tgz
BGE_REPO      := Xenova/bge-small-zh-v1.5

fetch-ai-models:
	@echo "==> Fetching local AI models into $(AI_MODELS_DIR)/ ..."
	@mkdir -p $(AI_MODELS_DIR)/bge-small-zh-v1.5
	@# 1) onnxruntime C 库（带版本号的真 .so，不是符号链接）
	@if [ -s "$(AI_MODELS_DIR)/libonnxruntime.so.$(ORT_VERSION)" ]; then \
		echo "    ✓ libonnxruntime.so.$(ORT_VERSION) already present, skip"; \
	else \
		echo "    ↓ onnxruntime-linux-x64-$(ORT_VERSION).tgz"; \
		tmp=$$(mktemp -d); \
		curl -fL --retry 3 -o $$tmp/ort.tgz "$(ORT_TGZ_URL)"; \
		tar -xzf $$tmp/ort.tgz -C $$tmp; \
		cp $$tmp/onnxruntime-linux-x64-$(ORT_VERSION)/lib/libonnxruntime.so.$(ORT_VERSION) \
			$(AI_MODELS_DIR)/libonnxruntime.so.$(ORT_VERSION); \
		rm -rf $$tmp; \
		echo "    ✓ libonnxruntime.so.$(ORT_VERSION)"; \
	fi
	@# 2) BGE-small-zh int8 量化模型
	@if [ -s "$(AI_MODELS_DIR)/bge-small-zh-v1.5/model_quantized.onnx" ]; then \
		echo "    ✓ bge-small-zh-v1.5/model_quantized.onnx already present, skip"; \
	else \
		echo "    ↓ bge-small-zh-v1.5/model_quantized.onnx"; \
		curl -fL --retry 3 -o $(AI_MODELS_DIR)/bge-small-zh-v1.5/model_quantized.onnx \
			"https://huggingface.co/$(BGE_REPO)/resolve/main/onnx/model_quantized.onnx"; \
		echo "    ✓ bge-small-zh-v1.5/model_quantized.onnx"; \
	fi
	@# 3) 词表（Xenova 仓库自带 vocab.txt，格式同 bert-base-chinese）
	@if [ -s "$(AI_MODELS_DIR)/bge-small-zh-v1.5/vocab.txt" ]; then \
		echo "    ✓ bge-small-zh-v1.5/vocab.txt already present, skip"; \
	else \
		echo "    ↓ bge-small-zh-v1.5/vocab.txt"; \
		curl -fL --retry 3 -o $(AI_MODELS_DIR)/bge-small-zh-v1.5/vocab.txt \
			"https://huggingface.co/$(BGE_REPO)/resolve/main/vocab.txt"; \
		echo "    ✓ bge-small-zh-v1.5/vocab.txt"; \
	fi
	@echo "==> AI models ready in $(AI_MODELS_DIR)/"

# 删除已下载的 AI 模型 / ONNX 运行时。
clean-ai-models:
	@echo "==> Removing $(AI_MODELS_DIR)/ ..."
	@rm -rf $(AI_MODELS_DIR)/

# 一键部署到远程服务器
deploy: docker-build
	@echo "==> Deploying to server (with gzip compression)..."
	@docker save studyquest-backend:latest | gzip | ssh -p 30901 ry@192.168.8.4 \
		"mkdir -p ~/data/studyquest-data/subtitles && \
		docker load && \
		{ docker stop studyquest-backend 2>/dev/null || true; } && \
		{ docker rm studyquest-backend 2>/dev/null || true; } && \
		docker run -d --name studyquest-backend --restart unless-stopped \
			-p 6001:8080 \
			--user \`id -u\`:\`id -g\` \
			-v ~/data/studyquest-data:/app/data \
			studyquest-backend:latest"
	@echo "==> Deployment complete!"
