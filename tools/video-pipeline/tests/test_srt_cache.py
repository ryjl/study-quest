"""Tests for srt_cache — the SRT-cache fast path.

Mirrors test_audio.py. The SRT cache is the FASTEST path in the worker: a
previous whisper run already produced the final SRT, so a HIT skips even the
transcription. These tests pin its contracts:
  - same (filename, file_size, model_name) → same SRT (stable, replayable)
  - different model_name → different SRT (switching models re-transcribes)
  - different file_size → different SRT (a re-encoded episode re-transcribes)
  - save then find round-trips the exact content
  - missing / empty / expired SRT → None (caller proceeds to whisper)
"""

from __future__ import annotations

import os
import time
from pathlib import Path

import srt_cache


SRT_SAMPLE = (
    "1\n"
    "00:00:00,000 --> 00:00:02,000\n"
    "你好世界\n\n"
    "2\n"
    "00:00:02,000 --> 00:00:04,000\n"
    "第二句话\n"
)


# ── key derivation ──────────────────────────────────────────────────────────


def test_cache_key_stable_for_same_inputs():
    """Same (filename, size, model) MUST produce the same key — this is what
    makes a re-enqueue hit the cache instead of re-running whisper."""
    k1 = srt_cache._cache_key("lesson01.mp4", 1000, "models/large-v3")
    k2 = srt_cache._cache_key("lesson01.mp4", 1000, "models/large-v3")
    assert k1 == k2


def test_cache_key_differs_on_model():
    """Switching whisper models invalidates the cache (the doc's hard rule).

    A different model is expected to produce a different transcript, so the old
    SRT must NOT be replayed.
    """
    k_v3 = srt_cache._cache_key("ep.mp4", 500, "models/large-v3")
    k_turbo = srt_cache._cache_key("ep.mp4", 500, "models/large-v3-turbo")
    assert k_v3 != k_turbo


def test_cache_key_differs_on_size():
    """A re-encoded episode (different byte size) must NOT reuse the old SRT."""
    k_small = srt_cache._cache_key("ep.mp4", 500, "models/large-v3")
    k_large = srt_cache._cache_key("ep.mp4", 5000, "models/large-v3")
    assert k_small != k_large


def test_cache_key_differs_on_filename():
    k_a = srt_cache._cache_key("a.mp4", 500, "models/large-v3")
    k_b = srt_cache._cache_key("b.mp4", 500, "models/large-v3")
    assert k_a != k_b


def test_cache_key_handles_none_size():
    """file_size is None when the backend doesn't know it — key must still be
    deterministic (not crash, same None → same key)."""
    k1 = srt_cache._cache_key("ep.mp4", None, "models/large-v3")
    k2 = srt_cache._cache_key("ep.mp4", None, "models/large-v3")
    assert k1 == k2
    # And distinct from a concrete size.
    assert k1 != srt_cache._cache_key("ep.mp4", 500, "models/large-v3")


# ── find / save round-trip ──────────────────────────────────────────────────


def test_find_returns_none_when_missing(tmp_path: Path):
    result = srt_cache.find_cached_srt(
        filename="nope.mp4", file_size=1, model_name="m", srt_cache_dir=str(tmp_path),
    )
    assert result is None


def test_save_then_find_roundtrips_content(tmp_path: Path):
    srt_cache.save_srt(
        filename="lesson01.mp4", file_size=1000, model_name="models/large-v3",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    got = srt_cache.find_cached_srt(
        filename="lesson01.mp4", file_size=1000, model_name="models/large-v3",
        srt_cache_dir=str(tmp_path),
    )
    assert got == SRT_SAMPLE


def test_find_misses_after_model_change(tmp_path: Path):
    """Saved with large-v3; querying for turbo must miss (different key)."""
    srt_cache.save_srt(
        filename="ep.mp4", file_size=500, model_name="models/large-v3",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    got = srt_cache.find_cached_srt(
        filename="ep.mp4", file_size=500, model_name="models/large-v3-turbo",
        srt_cache_dir=str(tmp_path),
    )
    assert got is None


def test_find_ignores_empty_file(tmp_path: Path):
    """A zero-byte SRT (e.g. a half-written file from a crash) is a miss."""
    key = srt_cache._cache_key("ep.mp4", 100, "m")
    (tmp_path / f"{key}.srt").write_text("", encoding="utf-8")
    result = srt_cache.find_cached_srt(
        filename="ep.mp4", file_size=100, model_name="m", srt_cache_dir=str(tmp_path),
    )
    assert result is None


# ── TTL / expiry ────────────────────────────────────────────────────────────


def test_find_expires_old_srt(tmp_path: Path):
    """An SRT older than DEFAULT_TTL_DAYS is treated as a miss AND deleted
    (lazy expiry)."""
    srt_cache.save_srt(
        filename="ep.mp4", file_size=100, model_name="m",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    # Backdate the file well past the TTL.
    key = srt_cache._cache_key("ep.mp4", 100, "m")
    path = tmp_path / f"{key}.srt"
    old = time.time() - (srt_cache.DEFAULT_TTL_DAYS + 1) * 86400
    os.utime(path, (old, old))

    result = srt_cache.find_cached_srt(
        filename="ep.mp4", file_size=100, model_name="m", srt_cache_dir=str(tmp_path),
    )
    assert result is None
    # Lazy expiry deletes the stale file.
    assert not path.exists()


def test_find_keeps_within_ttl(tmp_path: Path):
    """An SRT just barely inside the TTL is still a HIT."""
    srt_cache.save_srt(
        filename="ep.mp4", file_size=100, model_name="m",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    key = srt_cache._cache_key("ep.mp4", 100, "m")
    path = tmp_path / f"{key}.srt"
    # One day short of the TTL.
    age = time.time() - (srt_cache.DEFAULT_TTL_DAYS - 1) * 86400
    os.utime(path, (age, age))

    result = srt_cache.find_cached_srt(
        filename="ep.mp4", file_size=100, model_name="m", srt_cache_dir=str(tmp_path),
    )
    assert result == SRT_SAMPLE


# ── clean_old_srts ──────────────────────────────────────────────────────────


def test_clean_old_srts_removes_expired_keeps_fresh(tmp_path: Path):
    # Fresh SRT.
    srt_cache.save_srt(
        filename="fresh.mp4", file_size=100, model_name="m",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    # Stale SRT.
    srt_cache.save_srt(
        filename="stale.mp4", file_size=200, model_name="m",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    stale_key = srt_cache._cache_key("stale.mp4", 200, "m")
    stale_path = tmp_path / f"{stale_key}.srt"
    old = time.time() - (srt_cache.DEFAULT_TTL_DAYS + 5) * 86400
    os.utime(stale_path, (old, old))

    removed = srt_cache.clean_old_srts(str(tmp_path))
    assert removed == 1
    assert not stale_path.exists()
    # Fresh one survives.
    fresh_key = srt_cache._cache_key("fresh.mp4", 100, "m")
    assert (tmp_path / f"{fresh_key}.srt").exists()


def test_clean_old_srts_zero_when_nothing_stale(tmp_path: Path):
    srt_cache.save_srt(
        filename="fresh.mp4", file_size=100, model_name="m",
        srt_content=SRT_SAMPLE, srt_cache_dir=str(tmp_path),
    )
    assert srt_cache.clean_old_srts(str(tmp_path)) == 0
