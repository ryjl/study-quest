# Subtitle Worker (Whisper)

Consumes the StudyQuest subtitle queue: claims a job → finds a video source
(local cache or netdisk) → extracts 16 kHz mono audio with ffmpeg → transcribes
with faster-whisper → posts the SRT back. The resulting subtitles land in the
`subtitles` table and are immediately playable in the app.

Runs on the GPU desktop (WSL2, RTX 4060 8GB). The backend VPS only coordinates —
it never downloads video bytes.

> Full protocol/state-machine/concurrency docs: `docs/ai-subtitle-queue.md`.
> Real-environment pitfalls (cublas, 9P, tls, moov): §10 of that doc.

## Setup

```bash
cd tools/video-pipeline
cp config.example.yaml config.yaml      # edit paths to match your machine
make install                            # uv sync (creates .venv, installs deps + CUDA libs)
export SQ_INGEST_KEY=<backend's INGEST_KEY>   # skip if backend is keyless/LAN-only
make run
```

**Always launch via `make run` (or `./run.sh`), NOT `python worker.py` directly.**
`run.sh` exports `LD_LIBRARY_PATH` to the `nvidia-*-cu12` pip wheels' lib dirs
*before* python starts — glibc's dlopen reads it at process startup, so setting
it from Python is unreliable. Without it, `libcublas.so.12` won't be found.

The worker stays in the foreground and polls. Ctrl-C finishes the current job,
then exits.

## Configuration

`config.yaml` holds machine-specific paths (gitignored). `config.example.yaml`
is the annotated template (committed). The **ingest key is never in config.yaml**
— it comes from the `SQ_INGEST_KEY` environment variable.

Any scalar can also be overridden by an env var `SQ_SECTION__KEY`, e.g.
`SQ_WHISPER__COMPUTE_TYPE=int8_float16`. `~` and `$VAR` are expanded in paths.

### Video cache (local files)

`cache.dirs` is a list of directories to scan for already-downloaded videos. On
each job the worker looks for a file whose **filename + byte size** match the
episode (both come from the backend's claim response):

- **Match** → use the local file, skip the netdisk entirely (the signed download
  URL is never used, so its expiry window is irrelevant).
- **No match** → download from netdisk.

**WSL2 `/mnt/` paths**: `os.walk` fails on 9P-mounted Windows dirs (getdents64
returns EIO). The worker auto-detects `/mnt/` paths and uses `find` instead.
This is transparent — just list your `/mnt/e/...` dirs normally in config.

### Audio cache (extracted WAVs)

Extracted 16 kHz WAVs are cached under `~/.cache/sq-whisper/wav/`, keyed by
`(filename, file_size)`. A retry (after a failure) reuses the WAV instead of
re-downloading + re-extracting. Downloaded mp4s are cached there too. Old files
(>7 days) are purged at worker startup.

## Whisper prompt

faster-whisper's `initial_prompt` is **not** a free-form instruction — it's
decoder context (~244 token budget) that biases style and injects hot-words.
The worker composes it from three parts, all auto-derived from the claim
response:

1. **`base_prompt`** (config) — style引导, e.g. `"以下是普通话的句子。"`
2. **Course context** (auto) — subject / course title / chapter title. These
   are dense with subject terminology, suppressing Whisper's tendency to
   mistranscribe proper nouns.
3. **`ai_hint`** (admin-authored, per course) — anything针对性: a teacher's
   accent, the key theorem to listen for. Edit it in the admin course editor's
   "AI 提示" field. It also feeds the future quiz agent.

You don't write prompts per-video — set an `ai_hint` once per course and every
episode under it gets a tailored prompt automatically.

## GPU policy (hard requirement — NO fallback)

The worker **requires CUDA and never falls back to CPU**. If any of these fail,
the worker exits and you fix it by hand:

- No CUDA device → `gpu_preflight` aborts at startup.
- `device` misconfigured (not `cuda`) → aborts.
- Model load fails (incl. VRAM exhaustion) → exception propagates, worker exits.

Why no fallback: this is a batch worker on a personal machine. Silently
degrading to CPU would leave a job pretending to make progress while spending
hours — worse than failing loud so you can free VRAM or fix the config.

**4060 8GB**: Windows + Xwayland hold ~2.5 GB, leaving ~5.3 GB. `large-v3` at
`int8_float16` (~3 GB) fits single-worker. Don't run two workers — you'll OOM.

**`compute_type` must be `int8_float16`** (not `int8_fp16` — that's not a valid
ctranslate2 value and will crash at model load).

## Download strategy

When the video cache misses, the worker **downloads the full mp4 first**, then
extracts audio with ffmpeg from the local file. This sidesteps two problems with
streaming ffmpeg directly from the netdisk URL:

1. alist signs a URL that 302-redirects to a CDN (天翼云 OBS). ffmpeg's TLS
   handling drops the stream on the redirect.
2. MP4's moov atom sits at the file tail — ffmpeg streaming needs to HTTP-range-
   seek to the end, but the TLS stream has already dropped.

The download uses requests with **resume + retry** (HTTP Range, up to 5 attempts)
because the CDN's 400 KB/s traffic limit means a 145 MB video takes ~6 min, and
the TLS connection sometimes drops mid-way.

## Heartbeats & the reaper

While transcribing, a background thread POSTs `/heartbeat` every 30s, also
reporting progress ratio. The backend's reaper recovers a job silent for 30 min.
A `409` on `/complete` means the job was already finished by another worker —
the worker logs "stale" and drops its SRT (not an error).

## Tests

```bash
make test        # cache matching + prompt building (no GPU/network needed)
```

## Files

| file | role |
| :--- | :--- |
| `run.sh` | launcher: exports LD_LIBRARY_PATH for cublas, then exec python |
| `worker.py` | main loop, GPU preflight, heartbeat thread |
| `api_client.py` | the 4 worker-protocol endpoints + auth headers |
| `cache.py` | local-cache (filename+size) lookup, 9P-safe on /mnt/ |
| `audio.py` | ffmpeg WAV extraction + netdisk download (resume) + wav/mp4 cache |
| `transcriber.py` | faster-whisper wrapper + prompt builder + progress |
| `config.py` | YAML + env-var config loading |
