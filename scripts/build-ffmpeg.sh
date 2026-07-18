#!/usr/bin/env bash
# 在 debian:bookworm-slim 容器内编译 ffmpeg 7.1.1 的"现实够用"最小白名单配置。
# 由 `make fetch-ffmpeg` 通过 `docker run` 调用，产物 COPY 到挂载的 /out。
#
# 为什么自编译而不是用预编译：
# 1. ffbinaries / johnvansickle 全系列静态构建（gcc 8 / Debian 8.3.0-6 工具链）
#    在新内核上对任何网络协议输入（http/https URL）都 segfault（exit 139）。
#    probeMedia 走云盘 HTTPS URL 必崩。实测 v6.1 和 v7.0.2 都崩，是 gcc 8 静态
#    构建 + 新内核 socket 路径的 ABI 不兼容，不是版本问题。
# 2. BtbN 预编译虽不崩，但是"全量 codec"配置，ffmpeg+ffprobe 两文件 222MB。
#    本项目只用 ffprobe 读 metadata + ffmpeg 截一帧/提取封面（不解码、不转码），
#    95% 的编解码器是死重。
#
# 白名单覆盖现实可能遇到的所有视频/音频/容器格式：
#   容器:    mp4(mov) / mkv(matroska) / avi / wmv(asf) / flv / webm / mpegts / rm
#   视频解码: h264 / hevc / vp9 / vp8 / av1 / mpeg4 / mpeg2 / theora / wmv3 / vc1
#            / msmpeg4v3 / rv30 / rv40 / flv1 / mjpeg
#   音频解码: aac / mp3 / ac3 / eac3 / flac / opus / vorbis / pcm_s16le / pcm_s24le
#   编码:    mjpeg (截帧 JPEG 输出)
#
# 预期产物：~30MB 两个二进制（vs BtbN 全量 222MB / apt 459MB / ffbinaries 152MB）。
set -euo pipefail

echo "==> Installing build deps..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends \
    build-essential nasm yasm pkg-config curl xz-utils ca-certificates \
    libssl-dev zlib1g-dev \
    >/dev/null
rm -rf /var/lib/apt/lists/*

FFMPEG_VERSION=7.1.1
echo "==> Fetching ffmpeg ${FFMPEG_VERSION} source..."
cd /tmp
curl -fL --retry 3 https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz \
    | tar -xJ --strip-components=1

echo "==> Configuring (minimal whitelist)..."
./configure \
    --disable-everything --enable-static --disable-shared \
    --disable-ffplay --disable-doc --disable-programs --disable-debug \
    --enable-ffmpeg --enable-ffprobe \
    --enable-protocol=file,pipe,tcp,http,https \
    --enable-openssl --enable-zlib \
    --enable-demuxer=mov,matroska,avi,asf,flv,webm,mpegts,mpegvideo,rm,image2 \
    --enable-decoder=h264,hevc,vp9,vp8,av1,mpeg4,mpeg2video,theora,wmv3,vc1,msmpeg4v3,rv30,rv40,flv1,mjpeg \
    --enable-decoder=aac,mp3,ac3,eac3,flac,opus,vorbis,pcm_s16le,pcm_s24le \
    --enable-encoder=mjpeg \
    --enable-parser=h264,hevc,vp9,vc1,mpeg4video,mpegaudio,opus,vorbis \
    --enable-muxer=image2 \
    --enable-bsf=h264_mp4toannexb,hevc_mp4toannexb \
    --enable-filter=scale,format \
    --extra-cflags="-I/usr/include/openssl" \
    --extra-ldflags="-L/usr/lib/x86_64-linux-gnu"

echo "==> Building (this takes ~8 min)..."
make -j"$(nproc)" ffmpeg ffprobe

echo "==> Copying artifacts to /out..."
mkdir -p /out
cp ffmpeg ffprobe /out/
chmod +x /out/ffmpeg /out/ffprobe
ls -lh /out/ffmpeg /out/ffprobe
echo "==> Done."
