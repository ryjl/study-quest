#!/usr/bin/env bash
# Worker launcher: sets LD_LIBRARY_PATH to the nvidia-*-cu12 pip wheels' lib
# dirs BEFORE python starts, so ctranslate2's runtime dlopen of libcublas.so.12
# succeeds without a system CUDA toolkit install.
#
# Why a shell wrapper and not in-process os.environ: glibc's dlopen reads
# LD_LIBRARY_PATH at process startup; setting it from Python after the
# interpreter is up is unreliable across glibc/libc versions. Exporting it in
# the shell guarantees the dynamic linker sees it.
set -euo pipefail
cd "$(dirname "$0")"

VENV_DIR="$(pwd)/.venv"
NV_BASE="$VENV_DIR/lib/python3.12/site-packages/nvidia"

# Collect every nvidia/*/lib that exists.
NV_LIBS=""
for sub in cublas cuda_runtime cufft cuda_nvrtc nvjitlink; do
    d="$NV_BASE/$sub/lib"
    if [ -d "$d" ]; then
        if [ -z "$NV_LIBS" ]; then NV_LIBS="$d"; else NV_LIBS="$NV_LIBS:$d"; fi
    fi
done

if [ -n "$NV_LIBS" ]; then
    export LD_LIBRARY_PATH="$NV_LIBS${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi

exec "$VENV_DIR/bin/python" -u worker.py "$@"
