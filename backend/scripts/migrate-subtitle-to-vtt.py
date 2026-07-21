#!/usr/bin/env python3
"""One-shot migration: subtitles.srt_content → vtt_content + source + optimized.

Run AFTER stopping the backend server, BEFORE starting the new build. Idempotent
(re-running on an already-migrated DB is a no-op).

What it does:
  1. ALTER TABLE subtitles RENAME COLUMN srt_content → vtt_content
     (SQLite 3.25+ supports RENAME COLUMN; the bundled Go-compiled SQLite is new enough.)
  2. ALTER TABLE subtitles ADD COLUMN source TEXT NOT NULL DEFAULT 'whisper'
  3. ALTER TABLE subtitles ADD COLUMN optimized INTEGER NOT NULL DEFAULT 0
  4. Convert every row's VttContent from SRT to VTT format in-place. Existing
     subtitles came from whisper, so source='whisper' is correct for all of them.

Backup is taken before the migration runs (studyquest.db.bak.<timestamp>).

Usage:
    cd backend
    python3 scripts/migrate-subtitle-to-vtt.py [path/to/studyquest.db]

If no path is given, defaults to data/studyquest.db.
"""

from __future__ import annotations

import os
import shutil
import sqlite3
import sys
import time
from pathlib import Path


def srt_to_vtt(srt: str) -> str:
    """Mirror of subtitle.SrtToVtt in Go: trim + prepend WEBVTT header + ',' → '.'."""
    srt = (srt or "").strip()
    if not srt:
        return "WEBVTT\n\n"
    return "WEBVTT\n\n" + srt.replace(",", ".")


def main() -> int:
    db_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("data/studyquest.db")
    if not db_path.exists():
        print(f"ERROR: DB not found at {db_path}", file=sys.stderr)
        return 1

    # Idempotency check: if vtt_content column already exists, this script has
    # already run. Bail out safely.
    conn = sqlite3.connect(str(db_path))
    cols = {row[1] for row in conn.execute("PRAGMA table_info(subtitles)")}
    if "vtt_content" in cols:
        print(f"OK: {db_path} already migrated (vtt_content column present). No-op.")
        conn.close()
        return 0
    if "srt_content" not in cols:
        print(f"ERROR: subtitles table has neither srt_content nor vtt_content. "
              f"Expected one or the other. Got columns: {sorted(cols)}", file=sys.stderr)
        conn.close()
        return 2

    # Backup before touching anything.
    bak = db_path.with_suffix(db_path.suffix + f".bak.{time.strftime('%Y%m%d-%H%M%S')}")
    shutil.copy2(db_path, bak)
    print(f"backed up {db_path} → {bak}")

    # Phase 1: schema changes. Wrap in a transaction so a partial failure rolls back.
    print("renaming srt_content → vtt_content and adding source/optimized columns…")
    try:
        conn.execute("BEGIN")
        conn.execute("ALTER TABLE subtitles RENAME COLUMN srt_content TO vtt_content")
        conn.execute("ALTER TABLE subtitles ADD COLUMN source TEXT NOT NULL DEFAULT 'whisper'")
        conn.execute("ALTER TABLE subtitles ADD COLUMN optimized INTEGER NOT NULL DEFAULT 0")
        conn.execute("COMMIT")
    except Exception as e:
        conn.execute("ROLLBACK")
        print(f"schema migration failed, rolled back: {e}", file=sys.stderr)
        conn.close()
        return 3

    # Phase 2: convert SRT content to VTT in-place. SQLite doesn't have a REPLACE
    # for this kind of transform; we read all rows, transform in Python, write back.
    # 3 rows of 30-150 KB each is trivially small, no batching needed.
    print("converting each subtitle's SRT content to VTT…")
    rows = conn.execute("SELECT id, vtt_content FROM subtitles").fetchall()
    updated = 0
    for sid, content in rows:
        vtt = srt_to_vtt(content)
        conn.execute("UPDATE subtitles SET vtt_content = ? WHERE id = ?", (vtt, sid))
        updated += 1
    conn.commit()
    print(f"converted {updated} subtitle row(s)")

    conn.close()
    print(f"DONE: {db_path} migrated to VTT storage. Backup at {bak}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
