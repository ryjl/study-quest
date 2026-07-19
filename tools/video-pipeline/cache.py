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

WSL2 /mnt/ note: both `os.walk` (getdents64 syscall) AND `find` can fail on
9P-mounted Windows dirs with "[Errno 5] Input/output error" when reading the
directory entries. Crucially this affects ONLY the readdir path — `stat`/`open`
of a known full path uses different 9P messages (Twalk + Tgetattr/Topen) and
typically succeeds even mid-EIO. So a `find` that logs EIO may have silently
dropped some files from the index. To stay robust against this we use a TWO-
LAYER strategy: try the scanned index first (handles nested subdir layouts),
and on MISS fall back to a direct `stat(<cache_dir>/<filename>)` probe in each
configured dir (handles flat layouts where filename → path is just a join).
Native ext4 paths skip the stat fallback — they have no 9P, so a MISS really
means the file is absent.
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

    Note: `find` returns exit code 1 if readdir hits EIO on any subdir, even
    when stdout already contains the files it DID manage to read. We log that
    case as a warning so the operator knows the index may be partial (the
    lookup-level stat fallback in CacheIndex.lookup covers the gap for flat
    layouts).
    """
    try:
        r = subprocess.run(
            ["find", str(root), "-type", "f", "-printf", "%s\t%p\n"],
            capture_output=True, text=True, timeout=120,
        )
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        log.warning("cache: find failed on %s: %s — treating as empty", root, e)
        return []
    if r.returncode != 0:
        # find writes per-entry errors to stderr; surface them so the operator
        # sees WHY the index is partial (typically WSL2 9P EIO on big CJK dirs).
        err_tail = (r.stderr or "").strip().splitlines()[-1:] or ["<no stderr>"]
        log.warning(
            "cache: find on %s exited %d — index may be partial. stderr: %s",
            root, r.returncode, err_tail[0],
        )
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

    def _probe_flat(
        self, filename: str, expected_size: int | None,
    ) -> tuple[Path, int] | None:
        """Stat-fallback for WSL2 9P readdir EIO: try <cache_dir>/<filename>
        directly in each configured dir.

        Why this exists: on WSL2 /mnt/, `find`/`os.walk` can hit EIO reading a
        directory and silently drop some files from the index — even though the
        files are right there and `stat`/`open` work fine (different 9P path).
        For flat layouts (all videos directly under cache.dirs, no per-course
        subdirs) the full path is just `<dir>/<filename>`, so a direct stat
        recovers what readdir missed. This is a per-lookup O(num_dirs) cost,
        only paid on a miss, so it's cheap.

        Returns (path, size) on a size-matched (or size-unknown) hit, else None.
        Skipped entirely on native paths — they have no 9P, a miss is real.
        """
        for d in self._dirs:
            if not _is_wsl_mount(str(d)):
                continue
            candidate = d / filename
            try:
                st = candidate.stat()
            except FileNotFoundError:
                continue
            except OSError as e:
                # EIO on stat too — give up on this dir, try the next.
                log.info("cache: stat-fallback %s raised %s — skipping", candidate, e)
                continue
            if not st.st_size:
                continue
            if expected_size is not None and st.st_size != expected_size:
                log.info(
                    "cache: stat-fallback hit %s but size differs (have %d, want %d) — MISS",
                    candidate, st.st_size, expected_size,
                )
                continue
            return (candidate, st.st_size)
        return None

    def lookup(self, filename: str, expected_size: int | None) -> Path | None:
        """Return a cached path matching filename (+ size), or None.

        basename+size exact match is preferred. With expected_size=None we fall
        back to basename-only (best-effort, logged) since the backend may not
        have stored a size for the episode.

        Two-layer resolution:
          1. Try the scanned index (handles nested subdir layouts).
          2. On miss AND on WSL2 /mnt/ dirs, try direct stat(<dir>/<filename>)
             as a fallback against 9P readdir EIO (handles flat layouts).
        """
        if not filename:
            return None
        index = self._ensure()
        candidates = index.get(filename)
        if candidates:
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
                # Don't return None yet — stat fallback might find a DIFFERENT
                # same-named file (e.g. re-encoded copy in another dir) whose
                # size matches. Falls through to _probe_flat below.
            else:
                log.warning(
                    "cache: no file_size from backend, basename-only match for %r "
                    "(ambiguous if multiple episodes share this name)",
                    filename,
                )
                return candidates[0][0]
        # Index miss OR size-mismatched index hit: try the stat fallback on
        # WSL2 /mnt/ dirs. This is what recovers files that 9P readdir EIO
        # dropped from the index. On native ext4 dirs this is a no-op and the
        # miss stands (correctly — the file really isn't there).
        probed = self._probe_flat(filename, expected_size)
        if probed is not None:
            log.info(
                "cache: stat-fallback HIT %s (recovered from readdir EIO)",
                probed[0],
            )
            return probed[0]
        return None
