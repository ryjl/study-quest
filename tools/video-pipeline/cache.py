"""Local-cache lookup for video sources.

If the operator already has the target file on disk (downloaded once, sitting
in a known cache dir), the worker skips the netdisk download entirely — the
signed alist URL never gets used, so its expiry window is irrelevant. This is
the main robustness path: most study videos are already local, so the trickier
netdisk-download code path only runs for the occasional missing file.

Match policy (decided in brainstorm): basename + file_size double-match.
  - file_size present → the on-disk size must equal it exactly, else MISS
    (fall back to netdisk). Zero false positives at the cost of missing a file
    that was re-encoded to a different size.
  - file_size None (backend didn't store one) → basename-only match, with a
    warning: two episodes can share a basename ("第1讲.mp4" across courses), so
    this is best-effort.

The directory walk is memoized per worker process: personal cache dirs change
rarely, and re-walking thousands of files per job would be wasteful. Call
``reset()`` if you add files mid-run and want them picked up.

WSL2 /mnt/ note: Python's os.walk (getdents64 syscall) fails on 9P-mounted
Windows dirs with "[Errno 5] Input/output error". For /mnt/ paths we shell out
to `find` instead (coreutils has retry logic that handles 9P). Native ext4
paths use os.walk as before.
"""

from __future__ import annotations

import logging
import os
import subprocess
from pathlib import Path

log = logging.getLogger("sq.cache")


def _is_wsl_mount(path: str) -> bool:
    """True if the path is under a WSL2 Windows-disk mount (/mnt/c, /mnt/e, ...)."""
    return path.startswith("/mnt/")


def _scan_find(root: Path) -> list[tuple[Path, int]]:
    """Scan a directory tree using `find` (9P-safe on WSL2 /mnt/ paths).

    `find -printf "%s\t%p\\n"` gives size + full path in one pass. We parse the
    output; lines we can't parse are skipped (shouldn't happen, but defensive).
    """
    try:
        r = subprocess.run(
            ["find", str(root), "-type", "f", "-printf", "%s\t%p\n"],
            capture_output=True, text=True, timeout=120,
        )
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        log.warning("cache: find failed on %s: %s — treating as empty", root, e)
        return []
    results: list[tuple[Path, int]] = []
    for line in r.stdout.splitlines():
        # %s can be empty for special files; split once from the right (path may
        # contain tabs in theory, though unlikely for video files).
        size_str, _, path_str = line.partition("\t")
        if not path_str:
            continue
        try:
            size = int(size_str)
        except ValueError:
            continue
        results.append((Path(path_str), size))
    return results


def _scan_walk(root: Path) -> list[tuple[Path, int]]:
    """Scan a directory tree using os.walk (native ext4 — fast, no subprocess)."""
    results: list[tuple[Path, int]] = []
    for dirpath, _dirs, files in os.walk(root):
        for name in files:
            p = Path(dirpath, name)
            try:
                results.append((p, p.stat().st_size))
            except OSError:
                continue
    return results


class CacheIndex:
    def __init__(self, dirs: list[str]):
        self._dirs = [Path(d) for d in dirs]
        # filename → list of (path, size) candidates, built once on first use.
        self._index: dict[str, list[tuple[Path, int]]] | None = None

    def _build(self) -> dict[str, list[tuple[Path, int]]]:
        index: dict[str, list[tuple[Path, int]]] = {}
        for d in self._dirs:
            if not d.exists():
                log.warning("cache dir does not exist, skipping: %s", d)
                continue
            # WSL2 /mnt/ paths: os.walk (getdents64) fails on 9P. Use find.
            # Native paths: os.walk is faster (no subprocess).
            entries = _scan_find(d) if _is_wsl_mount(str(d)) else _scan_walk(d)
            for p, size in entries:
                index.setdefault(p.name, []).append((p, size))
            log.info("cache: indexed %d file(s) in %s", len(entries), d)
        return index

    def _ensure(self) -> dict[str, list[tuple[Path, int]]]:
        if self._index is None:
            self._index = self._build()
        return self._index

    def reset(self) -> None:
        """Drop the memoized index so the next lookup re-walks the dirs."""
        self._index = None

    def lookup(self, filename: str, expected_size: int | None) -> Path | None:
        """Return a cached path matching filename (+ size), or None.

        basename+size exact match is preferred. With expected_size=None we fall
        back to basename-only (best-effort, logged) since the backend may not
        have stored a size for the episode.
        """
        if not filename:
            return None
        index = self._ensure()
        candidates = index.get(filename)
        if not candidates:
            return None
        if expected_size is not None:
            exact = [p for p, sz in candidates if sz == expected_size]
            if exact:
                return exact[0]
            log.info(
                "cache: basename %r matched but size differs (have %s, want %d) — MISS",
                filename,
                ", ".join(str(sz) for _, sz in candidates),
                expected_size,
            )
            return None
        log.warning(
            "cache: no file_size from backend, basename-only match for %r "
            "(ambiguous if multiple episodes share this name)",
            filename,
        )
        return candidates[0][0]
