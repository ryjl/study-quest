"""Subtitle-queue worker main loop.

Polls the backend for subtitle jobs; for each one: resolve a video source
(local cache or netdisk URL) → ffmpeg extract 16kHz mono WAV → faster-whisper
transcribe → POST the SRT back. A background thread heartbeats claimed_at so the
backend's 30-min reaper doesn't recycle the job mid-transcription, and reports
progress so the admin UI can show how far along a long transcription is.

GPU is a hard precondition: if there's no CUDA device, the configured device is
wrong, or the model won't load (incl. VRAM exhaustion), we abort — no CPU
fallback. This is a batch worker on a personal machine; failing loud so a human
can intervene (free VRAM, fix the config) is more useful than silently spending
hours on a CPU transcription.

Usage:
    SQ_INGEST_KEY=secret python worker.py [--config config.yaml]
"""

from __future__ import annotations

import argparse
import logging
import os
import signal
import sys
import threading
import time

# IMPORTANT: launch via run.sh, not `python worker.py` directly.
#
# ctranslate2 dlopens libcublas.so.12 / libcudart.so.12 at runtime. On a system
# without a full CUDA toolkit install (e.g. WSL2 driver-only), the nvidia-*-cu12
# pip wheels ship those libs under site-packages/nvidia/*/lib. run.sh exports
# their paths into LD_LIBRARY_PATH BEFORE python starts — glibc's dlopen reads
# LD_LIBRARY_PATH at process startup, so setting it from Python after the
# interpreter is up (os.environ) is unreliable. The check below only WARNS if
# the env looks unset, so a direct `python worker.py` invocation fails loudly
# instead of silently crashing on the first transcription.
_nvidia_in_path = any(
    os.path.exists(os.path.join(d, "libcublas.so.12"))
    for d in os.environ.get("LD_LIBRARY_PATH", "").split(os.pathsep)
    if d
)
if not _nvidia_in_path:
    # Best-effort: try the in-process env set anyway (works on some glibc builds).
    try:
        import importlib.util as _ilu
        _spec = _ilu.find_spec("nvidia")
        if _spec and _spec.submodule_search_locations:
            _dirs = []
            for _root in _spec.submodule_search_locations:
                for _sub in ("cublas", "cuda_runtime", "cufft", "cuda_nvrtc", "nvjitlink"):
                    _d = os.path.join(_root, _sub, "lib")
                    if os.path.isdir(_d):
                        _dirs.append(_d)
            if _dirs:
                _existing = os.environ.get("LD_LIBRARY_PATH", "")
                os.environ["LD_LIBRARY_PATH"] = os.pathsep.join(_dirs) + (os.pathsep + _existing if _existing else "")
                print(f"[sq.worker] WARNING: LD_LIBRARY_PATH was unset — patched in-process for {len(_dirs)} nvidia dir(s). Use run.sh to set it reliably.", file=sys.stderr)
    except Exception:
        pass

import api_client
import audio
import cache
import config as cfg_mod
import transcriber
from api_client import ApiClient, StaleCompletion

log = logging.getLogger("sq.worker")


class Heartbeat:
    """Background thread that pings claimed_at + progress while a job runs."""

    def __init__(self, client: ApiClient, job_id: int, interval: float):
        self._client = client
        self._job_id = job_id
        self._interval = interval
        self._ratio: float | None = None
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._run, daemon=True, name="heartbeat")

    def start(self):
        self._thread.start()

    def set_ratio(self, ratio: float):
        self._ratio = ratio

    def stop(self):
        self._stop.set()

    def _run(self):
        while not self._stop.wait(self._interval):
            try:
                self._client.heartbeat(self._job_id, self._ratio)
            except Exception:
                log.warning("heartbeat failed (will retry)", exc_info=True)


def setup_logging():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(name)s] %(levelname)s %(message)s",
        datefmt="%H:%M:%S",
        stream=sys.stdout,
    )
    # Force unbuffered output so logs from a backgrounded worker are visible
    # immediately (Python buffers stdout when not attached to a tty).
    sys.stdout.reconfigure(line_buffering=True)
    sys.stderr.reconfigure(line_buffering=True)


def gpu_preflight() -> None:
    """Fail fast at startup if there's no usable CUDA device. No fallback."""
    import ctranslate2

    n = ctranslate2.get_cuda_device_count()
    if n <= 0:
        log.error(
            "no CUDA device (count=%d). Aborting — this worker requires a GPU "
            "and does not fall back to CPU.", n,
        )
        sys.exit(1)
    log.info("GPU preflight OK: %d CUDA device(s) visible", n)


def process_job(client: ApiClient, tr: transcriber.Transcriber, idx: cache.CacheIndex, job: api_client.ClaimedJob, cfg: cfg_mod.Config) -> None:
    log.info(
        "claimed job %d: %s (%s)",
        job.job_id, job.episode.title or f"episode {job.episode_id}", job.language,
    )

    # Source resolution: local video cache wins (skips the netdisk entirely).
    source: str = job.download_url
    headers = job.download_header
    hit = idx.lookup(job.episode.filename, job.episode.file_size)
    if hit is not None:
        log.info("cache HIT: %s (skipping netdisk download)", hit)
        source = str(hit)
        headers = None  # local file, no netdisk headers needed
    else:
        log.info("cache MISS — using netdisk URL for %r", job.episode.filename)

    hb = Heartbeat(client, job.job_id, cfg.worker.heartbeat_interval)
    hb.start()
    wav_path: str | None = None
    try:
        # The extracted WAV is cached by (filename, file_size): a retry won't
        # re-download or re-extract. We do NOT delete it after transcription —
        # it's the retry insurance. Stale ones get cleaned at worker startup.
        wav_path = audio.extract_wav(
            source,
            sample_rate=cfg.audio.sample_rate,
            channels=cfg.audio.channels,
            headers=headers,
            filename=job.episode.filename,
            file_size=job.episode.file_size,
            wav_cache_dir=cfg.audio.wav_cache_dir or None,
        )
        prompt = transcriber.build_prompt(job.episode, cfg.whisper.base_prompt)
        srt = tr.transcribe(wav_path, initial_prompt=prompt, on_progress=hb.set_ratio)
        try:
            client.complete(job.job_id, srt)
            log.info("job %d done: %d SRT bytes", job.job_id, len(srt))
        except StaleCompletion:
            # Not an error: another worker (or a reaper + re-claim) finished it.
            # Our SRT is stale — drop it and move on.
            log.info("job %d stale-completed by another worker; dropping SRT", job.job_id)
    finally:
        hb.stop()
        # Intentionally NOT deleting wav_path: it's the retry cache. The WAV
        # stays under wav_cache_dir and is reused if this job is re-enqueued.


def main() -> int:
    parser = argparse.ArgumentParser(description="StudyQuest subtitle worker")
    parser.add_argument("--config", default="config.yaml", help="path to config.yaml")
    args = parser.parse_args()

    setup_logging()
    cfg = cfg_mod.load(args.config)

    # Validate the bits we need before touching the GPU.
    # An empty ingest key is legal: it matches the backend's keyless LAN-only
    # mode (IngestKeyMiddleware is a no-op when key is ""). Only warn, so the
    # operator notices in a real deployment where a key was expected.
    if not cfg.backend.ingest_key:
        log.warning(
            "SQ_INGEST_KEY is not set — running keyless (only safe if the "
            "backend is also keyless/LAN-only)."
        )
    if not cfg.whisper.model_path:
        log.error("whisper.model_path is not set. Aborting.")
        return 1

    gpu_preflight()  # exits on failure

    # Purge stale WAVs from previous runs so the cache doesn't grow forever.
    audio.clean_old_wavs(cfg.audio.wav_cache_dir or None, cfg.audio.wav_cache_max_age_days)

    wid = cfg_mod.worker_id(cfg)
    log.info(
        "starting worker id=%s backend=%s model=%s",
        wid, cfg.backend.base_url, cfg.whisper.model_path,
    )

    client = ApiClient(cfg.backend.base_url, cfg.backend.ingest_key, wid)
    tr = transcriber.Transcriber(cfg.whisper)
    idx = cache.CacheIndex(cfg.cache.dirs)

    stop = threading.Event()

    def on_sig(signum, frame):
        log.info("received signal %d, shutting down after current job", signum)
        stop.set()

    signal.signal(signal.SIGINT, on_sig)
    signal.signal(signal.SIGTERM, on_sig)

    while not stop.is_set():
        try:
            job = client.claim()
        except Exception:
            log.error("claim failed", exc_info=True)
            stop.wait(cfg.worker.poll_interval)
            continue
        if job is None:
            stop.wait(cfg.worker.poll_interval)
            continue
        try:
            process_job(client, tr, idx, job, cfg)
        except Exception as e:
            # Any unhandled error in the pipeline → report to the backend and
            # move on. The admin UI shows it; retry/skip is a human decision.
            log.error("job %d failed: %s", job.job_id, e, exc_info=True)
            try:
                client.fail(job.job_id, str(e))
            except Exception:
                log.error("failed to report failure to backend", exc_info=True)

    log.info("worker stopped")
    return 0


if __name__ == "__main__":
    sys.exit(main())
