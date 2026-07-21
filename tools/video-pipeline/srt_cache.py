"""SRT cache for the whisper worker.

Mirrors the wav cache (audio.py), but stores the FINAL whisper output (the SRT)
instead of the extracted audio. This is the FASTEST path: a previous whisper
run already produced the SRT for this episode, so on a HIT we skip EVERYTHING
— wav cache check, video scan, netdisk download, ffmpeg, and the actual
transcription. We just POST the cached SRT back.

Key scheme: same (filename, file_size) base as the wav cache, but additionally
includes the whisper model name in the key. So switching models
(e.g. large-v3 → large-v3-turbo) auto-invalidates: a new model re-transcribes
instead of replaying an SRT produced by the old model. file_size in the key
also means a re-encoded episode won't reuse a stale SRT.

TTL: 30 days (longer than wav's 7 days, because SRT is smaller and more
valuable to reuse — re-extracting audio is cheap, re-running whisper isn't).
Old SRTs are also purged lazily at worker startup (see clean_old_srts).
"""

from __future__ import annotations

import hashlib
import logging
import os
import time
from pathlib import Path

log = logging.getLogger("sq.srt_cache")

DEFAULT_SRT_CACHE_DIR = "~/.cache/sq-whisper/srt"
DEFAULT_TTL_DAYS = 30.0


def _cache_key(filename: str, file_size: int | None, model_name: str) -> str:
    """Stable key for the SRT: (filename, file_size, model_name).

    model_name is part of the key (unlike the wav cache) so changing the whisper
    model auto-invalidates: a different model is expected to produce a different
    transcript, so we must not replay the old SRT.

    The key is INDEPENDENT of the video source (local cache vs netdisk URL), for
    the same reason as the wav cache: the SRT is the final product, and which
    physical source produced it doesn't matter.
    """
    ident = f"{filename}|{file_size or 'unknown'}|{model_name}"
    return hashlib.sha1(ident.encode("utf-8")).hexdigest()[:16]


def _srt_cache_dir(cfg_dir: str | None) -> Path:
    d = Path(os.path.expanduser(cfg_dir or DEFAULT_SRT_CACHE_DIR))
    d.mkdir(parents=True, exist_ok=True)
    return d


def find_cached_srt(
    *,
    filename: str,
    file_size: int | None,
    model_name: str,
    srt_cache_dir: str | None = None,
) -> str | None:
    """Return cached SRT content for this (filename, file_size, model_name), or None.

    This is the FAST PATH — faster even than the wav cache: it skips not just
    source resolution and ffmpeg but also the whisper transcription itself.
    Call this BEFORE find_cached_wav() in the worker.

    A hit is only returned for a non-empty file that is within TTL. An expired
    SRT is deleted here (lazy expiry) and treated as a miss.
    """
    cache_dir = _srt_cache_dir(srt_cache_dir)
    key = _cache_key(filename, file_size, model_name)
    path = cache_dir / f"{key}.srt"
    if not path.exists() or path.stat().st_size == 0:
        return None
    age_days = (time.time() - path.stat().st_mtime) / 86400
    if age_days > DEFAULT_TTL_DAYS:
        log.info("srt cache EXPIRED: %s (%.1f days old)", path.name, age_days)
        try:
            path.unlink()
        except OSError:
            pass
        return None
    log.info("srt cache HIT: %s (%d bytes)", path.name, path.stat().st_size)
    return path.read_text(encoding="utf-8")


def save_srt(
    *,
    filename: str,
    file_size: int | None,
    model_name: str,
    srt_content: str,
    srt_cache_dir: str | None = None,
) -> None:
    """Save SRT to the cache for future reuse.

    Call this AFTER a successful transcription, BEFORE client.complete() — so
    that the cache is populated even if the complete POST fails (e.g. a network
    blip), and a retry of the same job hits the cache instead of re-running
    whisper.
    """
    cache_dir = _srt_cache_dir(srt_cache_dir)
    key = _cache_key(filename, file_size, model_name)
    path = cache_dir / f"{key}.srt"
    path.write_text(srt_content, encoding="utf-8")
    log.info("srt cache SAVED: %s (%d bytes)", path.name, len(srt_content))


def clean_old_srts(srt_cache_dir: str | None, max_age_days: float = DEFAULT_TTL_DAYS) -> int:
    """Delete SRTs older than max_age_days. Returns how many were removed.

    Called once at worker startup so the cache doesn't grow forever.
    """
    d = _srt_cache_dir(srt_cache_dir)
    cutoff = time.time() - max_age_days * 86400
    removed = 0
    for f in d.glob("*.srt"):
        try:
            if f.stat().st_mtime < cutoff:
                f.unlink()
                removed += 1
        except OSError:
            pass
    if removed:
        log.info("srt cache: removed %d stale file(s) older than %.0f days", removed, max_age_days)
    return removed
