"""Tests for audio.find_cached_wav — the WAV-cache fast path.

The worker calls find_cached_wav BEFORE resolving the video source, so a HIT
must skip the video cache scan entirely. These tests pin that contract:
  - same (filename, file_size) → same wav path (stable across source changes)
  - different file_size → different wav path (so a re-encoded episode doesn't
    accidentally reuse the old wav)
  - missing wav → None (caller proceeds to video cache / netdisk)
"""

from __future__ import annotations

from pathlib import Path

import audio


def test_find_cached_wav_hit(tmp_path: Path):
    # Pre-populate the wav cache for a known (filename, file_size) key.
    key = audio._cache_key("lesson01.mp4", 1000, source="")
    (tmp_path / f"{key}.wav").write_bytes(b"x" * 100)

    result = audio.find_cached_wav(
        filename="lesson01.mp4", file_size=1000, wav_cache_dir=str(tmp_path),
    )
    assert result is not None
    assert result.endswith(f"{key}.wav")


def test_find_cached_wav_miss(tmp_path: Path):
    result = audio.find_cached_wav(
        filename="nonexistent.mp4", file_size=999, wav_cache_dir=str(tmp_path),
    )
    assert result is None


def test_cache_key_independent_of_source(tmp_path: Path):
    """Same (filename, file_size) produces the same wav regardless of source.

    This is the property that lets the worker check the wav cache BEFORE
    deciding the video source — a HIT from a previous netdisk run is reused
    when the source is now a local cache path, and vice versa.
    """
    key_local = audio._cache_key("ep.mp4", 500, source="/local/path/ep.mp4")
    key_url = audio._cache_key("ep.mp4", 500, source="https://netdisk/.../ep.mp4")
    key_none = audio._cache_key("ep.mp4", 500, source="")
    assert key_local == key_url == key_none


def test_cache_key_differs_on_size(tmp_path: Path):
    """A re-encoded episode (different byte size) must NOT reuse the old wav."""
    key_small = audio._cache_key("ep.mp4", 500, source="")
    key_large = audio._cache_key("ep.mp4", 5000, source="")
    assert key_small != key_large


def test_find_cached_wav_ignores_empty_file(tmp_path: Path):
    """A zero-byte wav (failed extraction leftover) is treated as a miss."""
    key = audio._cache_key("ep.mp4", 100, source="")
    (tmp_path / f"{key}.wav").write_bytes(b"")
    result = audio.find_cached_wav(
        filename="ep.mp4", file_size=100, wav_cache_dir=str(tmp_path),
    )
    assert result is None
