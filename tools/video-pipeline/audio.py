"""Audio extraction via ffmpeg, with a local WAV cache.

Whisper needs 16 kHz mono PCM. We extract to a WAV on disk (whisper needs a
seekable input, so we can't just pipe). The input is either a local cache path
(cache HIT) or a netdisk direct link (cache MISS).

WAV cache: the extracted WAV is kept under ``wav_cache_dir`` (default
``~/.cache/sq-whisper/wav``), keyed by a stable hash of (filename, file_size).
On a re-run (retry after a failure, or a re-enqueue) the WAV is reused instead
of re-downloading + re-extracting from the netdisk. This matters because netdisk
direct links expire and the download is the expensive, flaky part — never repeat
it for the same episode. Old WAVs are cleaned up lazily (see worker startup).

Known risk with URL inputs: MP4 stores its moov atom at the file tail, so
ffmpeg reading an HTTPS stream wants to HTTP-range-seek to the end. Some
netdisk direct links don't support range requests → ffmpeg fails. The video
cache (cache.py) is the primary mitigation; the WAV cache means a retry after
such a failure doesn't re-hit the netdisk.
"""

from __future__ import annotations

import hashlib
import logging
import os
import subprocess
import time
import tempfile
from pathlib import Path

log = logging.getLogger("sq.audio")

DEFAULT_WAV_CACHE_DIR = "~/.cache/sq-whisper/wav"


def _cache_key(filename: str, file_size: int | None, source: str) -> str:
    """Stable key for the WAV: prefer (filename, size); fall back to source hash."""
    if filename:
        ident = f"{filename}|{file_size or 'unknown'}"
    else:
        ident = source
    return hashlib.sha1(ident.encode("utf-8")).hexdigest()[:16]


def _wav_cache_dir(cfg_dir: str | None) -> Path:
    d = Path(os.path.expanduser(cfg_dir or DEFAULT_WAV_CACHE_DIR))
    d.mkdir(parents=True, exist_ok=True)
    return d


def extract_wav(
    source: str,
    sample_rate: int = 16000,
    channels: int = 1,
    headers: dict[str, str] | None = None,
    tmp_dir: str | None = None,
    *,
    filename: str = "",
    file_size: int | None = None,
    wav_cache_dir: str | None = None,
) -> str:
    """Extract a 16 kHz mono WAV from a local path or http(s) URL.

    If a cached WAV for this (filename, file_size) already exists and is
    non-empty, it is reused — skipping the netdisk download + ffmpeg entirely.
    Returns the path to the WAV (cached or fresh). The caller should NOT remove
    it (it's kept for retries); pass ``cleanup=False`` semantics by not calling
    cleanup() in the worker for cached WAVs.
    """
    cache_dir = _wav_cache_dir(wav_cache_dir)
    key = _cache_key(filename, file_size, source)
    wav_path = cache_dir / f"{key}.wav"

    if wav_path.exists() and wav_path.stat().st_size > 0:
        log.info("wav cache HIT: %s (%d bytes)", wav_path.name, wav_path.stat().st_size)
        return str(wav_path)

    # For http(s) inputs, ffmpeg can't reliably stream-read an MP4: the moov
    # atom sits at the file tail, so ffmpeg must HTTP-range-seek to the end, but
    # the alist→CDN 302 redirect's TLS stream drops mid-seek ("IO error: End of
    # file" → "moov atom not found"). Downloading the whole file first sidesteps
    # both the range and the TLS issues. The downloaded mp4 is cached too (same
    # key) so a retry doesn't re-download.
    input_path = source
    downloaded_mp4: Path | None = None
    if source.startswith(("http://", "https://")):
        mp4_path = cache_dir / f"{key}.mp4"
        if mp4_path.exists() and mp4_path.stat().st_size > 0:
            log.info("mp4 cache HIT: %s (%d bytes)", mp4_path.name, mp4_path.stat().st_size)
        else:
            log.info("downloading video: %s", _describe_source(source))
            _download(source, mp4_path, headers)
            log.info("downloaded %d bytes → %s", mp4_path.stat().st_size, mp4_path.name)
        input_path = str(mp4_path)
        downloaded_mp4 = mp4_path

    # Build the ffmpeg command (local file input — no streaming quirks).
    cmd: list[str] = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y"]
    cmd += [
        "-i", input_path,
        "-vn",                     # no video
        "-ac", str(channels),      # mono
        "-ar", str(sample_rate),   # resample
        "-f", "wav",
        str(wav_path),
    ]

    log.info("ffmpeg extracting audio from %s", "cached mp4" if downloaded_mp4 else _describe_source(source))
    try:
        subprocess.run(cmd, check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as e:
        # Remove a partial/empty wav so the next retry tries again. Keep the mp4
        # (it downloaded fine; the failure is in audio extraction, not download).
        try:
            wav_path.unlink()
        except OSError:
            pass
        raise RuntimeError(f"ffmpeg failed: {e.stderr.strip() or e}") from None
    return str(wav_path)


def _download(url: str, dest: Path, headers: dict[str, str] | None = None) -> None:
    """Stream-download a URL to a file, following redirects, with resume.

    The alist→天翼云 OBS CDN enforces a ~400KB/s traffic limit, so a 145MB video
    takes ~6 min — long enough that the TLS connection sometimes drops mid-way
    ("SSL: UNEXPECTED_EOF_WHILE_READING"). We resume from the byte we reached
    via HTTP Range, retrying up to MAX_RETRIES times. The partial file is kept
    across retries (in a .part file) so progress isn't lost.
    """
    import requests

    MAX_RETRIES = 5
    part = dest.with_suffix(dest.suffix + ".part")
    # Resume from whatever we already have in the .part file.
    have = part.stat().st_size if part.exists() else 0
    for attempt in range(1, MAX_RETRIES + 1):
        req_headers = dict(headers or {})
        if have > 0:
            req_headers["Range"] = f"bytes={have}-"
        try:
            with requests.get(url, headers=req_headers, stream=True, timeout=60, allow_redirects=True) as r:
                r.raise_for_status()
                # 200 = server ignored Range (full file); 206 = partial (resumed).
                mode = "ab" if (have > 0 and r.status_code == 206) else "wb"
                if mode == "wb":
                    have = 0  # server sent the whole thing; reset offset
                with part.open(mode) as f:
                    for chunk in r.iter_content(chunk_size=1024 * 1024):
                        if chunk:
                            f.write(chunk)
                            have += len(chunk)
            # If we got here without exception, the download completed.
            part.rename(dest)
            return
        except Exception as e:
            log.warning("download attempt %d/%d failed at %d bytes: %s", attempt, MAX_RETRIES, have, e)
            if attempt == MAX_RETRIES:
                try:
                    part.unlink()
                except OSError:
                    pass
                raise
            log.info("retrying download from byte %d...", have)
            time.sleep(2 * attempt)  # backoff


def _describe_source(source: str) -> str:
    """Short log label: full path for local, redacted-ish for URLs."""
    if source.startswith(("http://", "https://")):
        from urllib.parse import urlparse

        p = urlparse(source)
        tail = p.path.rsplit("/", 1)[-1]
        return f"<netdisk> {p.netloc}/.../{tail}"
    return source


def cleanup(wav_path: str) -> None:
    """Remove a WAV. No-op for cached paths that don't exist anymore.

    NOTE: by default the worker keeps WAVs (they're the retry cache). Only call
    this for orphaned temp files, not for the normal post-transcribe path.
    """
    try:
        os.remove(wav_path)
    except OSError:
        pass


def clean_old_wavs(wav_cache_dir: str | None, max_age_days: float = 7.0) -> int:
    """Delete WAVs older than max_age_days. Returns how many were removed.

    Called once at worker startup so the cache doesn't grow forever.
    """
    import time

    d = _wav_cache_dir(wav_cache_dir)
    cutoff = time.time() - max_age_days * 86400
    removed = 0
    for f in d.glob("*.wav"):
        try:
            if f.stat().st_mtime < cutoff:
                f.unlink()
                removed += 1
        except OSError:
            pass
    if removed:
        log.info("wav cache: removed %d stale file(s) older than %.0f days", removed, max_age_days)
    return removed
