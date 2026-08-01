.PHONY: build-admin build run run-admin test test-admin docker-build docker-run clean migrate build-apk build-apk-arm64 build-apk-arm build-apk-x64 build-apk-fat build-tv-apk build-tv-apk-debug install-tv fetch-ai-models clean-ai-models fetch-ffmpeg clean-ffmpeg

# 本地 deploy 配置(不进 git):把你的 DEPLOY_HOST/DEPLOY_USER 等写进 .deploy.env,
# make deploy 自动读取。模板见 .deploy.env.example。
-include .deploy.env

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

# 构建 Docker 镜像（多阶段：Node 构建 SPA → Go 构建二进制 → debian-slim 运行时）
# 注意：build context 是仓库根，这样 SPA 阶段才能访问 frontend-admin/ 源码。
# 依赖 fetch-ffmpeg：若本地还没编过 ffmpeg/ffprobe，先编（~8min 首次，之后秒级
# COPY）。fetch-ffmpeg 本身幂等，已存在就跳过。
docker-build: fetch-ffmpeg
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

# ─── TV 原生 (Android Kotlin + Compose for TV) ──────────────────────────
# 与上面的 Flutter build-apk 是两个独立工程(工具链不同,故分开 make 目标,不硬拼):
#   build-apk*  → frontend/ (Flutter, PAD/手机)
#   build-tv-*  → tv-android/ (Kotlin 原生, Android TV)
#
# ANDROID_HOME 需指向 Android SDK(本机默认 ~/android-sdk)。gradlew 自带 wrapper,
# 不需要预装 Gradle。JDK 17 必需。
TV_DIR := tv-android
ANDROID_HOME ?= $(HOME)/android-sdk

# 构建 TV release APK(单 fat,含所有 ABI)。产物在 tv-android/app/build/outputs/apk/release/。
# TV 端目前不 split-per-abi(TV 盒子基本是 arm64,且 media3 native 库已较大,先 fat 简化)。
build-tv-apk:
	@echo "==> Building TV release APK (Kotlin + Compose for TV)..."
	@cd $(TV_DIR) && ANDROID_HOME=$(ANDROID_HOME) ANDROID_SDK_ROOT=$(ANDROID_HOME) ./gradlew :app:assembleRelease
	@echo "✓ TV APK at $(TV_DIR)/app/build/outputs/apk/release/"

# 构建 TV debug APK(开发调试用,applicationId 带 .debug 后缀)。
build-tv-apk-debug:
	@echo "==> Building TV debug APK..."
	@cd $(TV_DIR) && ANDROID_HOME=$(ANDROID_HOME) ANDROID_SDK_ROOT=$(ANDROID_HOME) ./gradlew :app:assembleDebug
	@echo "✓ TV debug APK at $(TV_DIR)/app/build/outputs/apk/debug/"

# 构建 debug APK 并装到已连接的 TV 设备/模拟器(adb)。
install-tv: build-tv-apk-debug
	@echo "==> Installing TV debug APK to connected device..."
	@cd $(TV_DIR) && ANDROID_HOME=$(ANDROID_HOME) $(ANDROID_HOME)/platform-tools/adb install -r app/build/outputs/apk/debug/app-debug.apk

# 清理构建产物。
# 注意：`rm -rf backend/data/` 会顺带删掉 ai-models/ 和 ffmpeg-bin/ —— 这是 backend/data
# 被 .gitignore 整个覆盖、且作为本地缓存目录的设计意图。重跑时 make fetch-ai-models
# 和 make fetch-ffmpeg 会重新拉/编。
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

# ─── 本地自编译 ffmpeg / ffprobe（最小白名单配置）─────────────────────────
# 在 debian:bookworm-slim 容器内编译 ffmpeg 7.1.1 的"现实够用"白名单配置，产物
# 落到 backend/data/ffmpeg-bin/（已被 .gitignore，二进制不进 git）。Dockerfile COPY
# 这个目录进去，所以 docker build 永远不重编 ffmpeg——只在删除目录或改 configure
# 选项后才需要重新跑这个 target。
#
# 为什么自编译而不是用预编译：
#   1. ffbinaries / johnvansickle 全系列静态构建（gcc 8 工具链）在新内核上对任何
#      网络协议输入（http/https URL）都 segfault (exit 139)——probeMedia 走云盘
#      HTTPS URL 必崩。是静态 glibc 在新内核 socket 路径的 ABI 问题，不是版本问题。
#   2. BtbN 预编译虽不崩，但是"全量 codec"配置，ffmpeg+ffprobe 两文件 222MB——
#      项目只用 ffprobe 读 metadata + ffmpeg 截一帧/提取封面，95% codec 是死重。
#   自编译最小白名单 ~17MB（两文件），规避 segfault，且省 ~200MB 镜像空间。
#
# 配置详见 scripts/build-ffmpeg.sh。改配置后：`make clean-ffmpeg && make fetch-ffmpeg`。
FFMPEG_BIN_DIR := backend/data/ffmpeg-bin

fetch-ffmpeg:
	@echo "==> Ensuring minimal ffmpeg/ffprobe present in $(FFMPEG_BIN_DIR)/ ..."
	@if [ -x "$(FFMPEG_BIN_DIR)/ffmpeg" ] && [ -x "$(FFMPEG_BIN_DIR)/ffprobe" ]; then \
		echo "    ✓ ffmpeg/ffprobe already built, skip (delete $(FFMPEG_BIN_DIR)/ to rebuild)"; \
	else \
		echo "    ↓ Building ffmpeg 7.1.1 minimal whitelist (~8 min on first run)..."; \
		mkdir -p $(FFMPEG_BIN_DIR); \
		docker run --rm \
			--platform linux/amd64 \
			-v $(PWD)/scripts/build-ffmpeg.sh:/build.sh:ro \
			-v $(PWD)/$(FFMPEG_BIN_DIR):/out \
			debian:bookworm-slim bash /build.sh; \
		echo "    ✓ $(FFMPEG_BIN_DIR)/ffmpeg ($$(ls -lh $(FFMPEG_BIN_DIR)/ffmpeg | awk '{print $$5}'))"; \
		echo "    ✓ $(FFMPEG_BIN_DIR)/ffprobe ($$(ls -lh $(FFMPEG_BIN_DIR)/ffprobe | awk '{print $$5}'))"; \
	fi
	@echo "==> ffmpeg/ffprobe ready in $(FFMPEG_BIN_DIR)/"

# 删除本地编译的 ffmpeg/ffprobe（换 configure 选项或 ffmpeg 版本时先跑这个）。
clean-ffmpeg:
	@echo "==> Removing $(FFMPEG_BIN_DIR)/ ..."
	@rm -rf $(FFMPEG_BIN_DIR)/

# 一键部署到远程服务器。目标机器配置通过环境变量提供(不写死在 Makefile 里):
#   DEPLOY_HOST      远程主机(IP/域名)
#   DEPLOY_USER      ssh 用户名
#   DEPLOY_SSH_PORT  ssh 端口(默认 22)
#   DEPLOY_PORT      容器在远程主机暴露的端口(映射到容器内 8080)
# 例: make deploy DEPLOY_HOST=192.168.1.10 DEPLOY_USER=me
# 或把这些 export 到 shell 配置里,make deploy 直接用。
DEPLOY_HOST     ?=
DEPLOY_USER     ?=
DEPLOY_SSH_PORT ?= 22
DEPLOY_PORT     ?= 8080

deploy: docker-build
	@if [ -z "$(DEPLOY_HOST)" ] || [ -z "$(DEPLOY_USER)" ]; then \
		echo "ERROR: DEPLOY_HOST 和 DEPLOY_USER 必须设置。例:make deploy DEPLOY_HOST=1.2.3.4 DEPLOY_USER=me"; \
		exit 1; \
	fi
	@echo "==> Deploying to $(DEPLOY_USER)@$(DEPLOY_HOST):$(DEPLOY_SSH_PORT) (host port $(DEPLOY_PORT))..."
	@docker save studyquest-backend:latest | gzip | ssh -p $(DEPLOY_SSH_PORT) $(DEPLOY_USER)@$(DEPLOY_HOST) \
		"mkdir -p ~/data/studyquest-data/subtitles && \
		docker load && \
		{ docker stop studyquest-backend 2>/dev/null || true; } && \
		{ docker rm studyquest-backend 2>/dev/null || true; } && \
		docker run -d --name studyquest-backend --restart unless-stopped \
			-p $(DEPLOY_PORT):8080 \
			--user \`id -u\`:\`id -g\` \
			-e AI_MODELS_DIR=/app/ai-models \
			-v ~/data/studyquest-data:/app/data \
			studyquest-backend:latest"
	@echo "==> Deployment complete!"
