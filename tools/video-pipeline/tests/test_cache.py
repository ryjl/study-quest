"""Tests for the cache index (basename + size matching)."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

import cache


@pytest.fixture
def cache_tree(tmp_path: Path) -> Path:
    """Build a fake cache dir tree with a few files."""
    (tmp_path / "数学").mkdir()
    (tmp_path / "物理").mkdir()
    (tmp_path / "数学" / "第1讲.mp4").write_bytes(b"x" * 1000)
    (tmp_path / "物理" / "第1讲.mp4").write_bytes(b"y" * 2000)  # same name, diff size
    (tmp_path / "数学" / "第2讲.mp4").write_bytes(b"z" * 3000)
    return tmp_path


def test_hit_with_matching_size(cache_tree: Path):
    idx = cache.CacheIndex([str(cache_tree)])
    hit = idx.lookup("第1讲.mp4", 1000)
    assert hit is not None
    assert hit.name == "第1讲.mp4"
    assert hit.parent.name == "数学"  # the size-matched one, not 物理


def test_miss_when_size_differs(cache_tree: Path):
    """basename matches but no candidate has the expected size → MISS."""
    idx = cache.CacheIndex([str(cache_tree)])
    assert idx.lookup("第1讲.mp4", 9999) is None


def test_basename_only_when_size_unknown(cache_tree: Path, caplog):
    """file_size=None falls back to basename-only (best-effort)."""
    idx = cache.CacheIndex([str(cache_tree)])
    with caplog.at_level("WARNING"):
        hit = idx.lookup("第1讲.mp4", None)
    assert hit is not None
    assert "basename-only" in caplog.text  # warned it's ambiguous


def test_miss_when_filename_absent(cache_tree: Path):
    idx = cache.CacheIndex([str(cache_tree)])
    assert idx.lookup("不存在的.mp4", None) is None


def test_multiple_dirs(tmp_path: Path):
    d1 = tmp_path / "d1"
    d2 = tmp_path / "d2"
    d1.mkdir()
    d2.mkdir()
    (d1 / "ep.mp4").write_bytes(b"a" * 500)
    (d2 / "ep.mp4").write_bytes(b"b" * 500)
    idx = cache.CacheIndex([str(d1), str(d2)])
    hit = idx.lookup("ep.mp4", 500)
    assert hit is not None
    # Either is a valid match; both have the right size.
    assert hit.name == "ep.mp4"


def test_empty_filename_returns_none(cache_tree: Path):
    idx = cache.CacheIndex([str(cache_tree)])
    assert idx.lookup("", 1000) is None


def test_nonexistent_dir_skipped(tmp_path: Path, caplog):
    idx = cache.CacheIndex([str(tmp_path / "nope")])
    with caplog.at_level("WARNING"):
        assert idx.lookup("anything.mp4", None) is None
    assert "does not exist" in caplog.text


def test_index_is_memoized(cache_tree: Path):
    """The walk runs once; adding a file mid-run isn't seen until reset()."""
    idx = cache.CacheIndex([str(cache_tree)])
    assert idx.lookup("第3讲.mp4", None) is None
    # Add a new file after the index was built.
    (cache_tree / "第3讲.mp4").write_bytes(b"new")
    assert idx.lookup("第3讲.mp4", None) is None  # still memoized
    idx.reset()
    assert idx.lookup("第3讲.mp4", None) is not None  # re-walked


# --- stat-fallback (WSL2 9P readdir EIO recovery) ---
# On WSL2 /mnt/, find/os.walk can silently drop files from the index due to
# 9P EIO on readdir — but stat/open of a known full path still works (different
# 9P message). The lookup-level stat fallback recovers these for flat layouts.
# We monkeypatch _is_wsl_mount to True so the fallback runs under tmp_path.

def test_stat_fallback_recovers_file_missing_from_index(tmp_path: Path, monkeypatch):
    """Simulate 9P readdir EIO: index is empty but the file exists on disk.

    The stat fallback should find <dir>/<filename> directly.
    """
    monkeypatch.setattr(cache, "_is_wsl_mount", lambda _p: True)
    # Place a file on disk but DON'T let the index see it: build the index with
    # an empty dir, then add the file afterward (memoization freezes the index).
    idx = cache.CacheIndex([str(tmp_path)])
    idx._ensure()  # builds empty index
    (tmp_path / "26-7-12【复盘】.mp4").write_bytes(b"x" * 354171945)
    hit = idx.lookup("26-7-12【复盘】.mp4", 354171945)
    assert hit is not None
    assert hit.name == "26-7-12【复盘】.mp4"


def test_stat_fallback_respects_size_mismatch(tmp_path: Path, monkeypatch):
    """Stat fallback finds the file but size differs → still MISS."""
    monkeypatch.setattr(cache, "_is_wsl_mount", lambda _p: True)
    idx = cache.CacheIndex([str(tmp_path)])
    idx._ensure()
    (tmp_path / "ep.mp4").write_bytes(b"x" * 1000)
    # Index is empty (built before file existed), so only stat fallback can see it.
    assert idx.lookup("ep.mp4", 9999) is None  # wrong size → MISS


def test_stat_fallback_skipped_on_native_paths(tmp_path: Path, monkeypatch):
    """On native ext4 paths (not /mnt/), stat fallback is skipped — a miss is real."""
    # _is_wsl_mount not patched → returns False for tmp_path.
    idx = cache.CacheIndex([str(tmp_path)])
    idx._ensure()
    (tmp_path / "ep.mp4").write_bytes(b"x" * 1000)
    # Index is empty AND no fallback → genuine miss.
    assert idx.lookup("ep.mp4", 1000) is None


def test_stat_fallback_tries_multiple_dirs(tmp_path: Path, monkeypatch):
    """File in the second configured dir is found by stat fallback."""
    monkeypatch.setattr(cache, "_is_wsl_mount", lambda _p: True)
    d1 = tmp_path / "d1"
    d2 = tmp_path / "d2"
    d1.mkdir()
    d2.mkdir()
    idx = cache.CacheIndex([str(d1), str(d2)])
    idx._ensure()  # both empty
    (d2 / "ep.mp4").write_bytes(b"x" * 500)
    hit = idx.lookup("ep.mp4", 500)
    assert hit is not None
    assert hit.parent.name == "d2"
