"""HTTP client for the subtitle-queue worker protocol.

Four endpoints (see docs/ai-subtitle-queue.md §4.2), all gated by the
``X-Ingest-Key`` header (shared pre-shared key, same one the ingest toolchain
uses). The worker self-identifies via ``X-Worker-ID`` for observability.

  POST /api/v1/subtitle-jobs/claim          → ClaimedJob | None
  POST /api/v1/subtitle-jobs/:id/complete   {srt_content}
  POST /api/v1/subtitle-jobs/:id/heartbeat  {progress_ratio?}
  POST /api/v1/subtitle-jobs/:id/fail       {error}
"""

from __future__ import annotations

from dataclasses import dataclass

import requests


class StaleCompletion(Exception):
    """The job is no longer processing (409) — another worker already finished
    it, or a reaper recycled it. The SRT we hold is stale and MUST be dropped.
    This is not an error to report back to the server."""


@dataclass
class EpisodeInfo:
    title: str
    duration_seconds: int | None
    filename: str  # basename, e.g. "lesson01.mp4" — cache-match key
    file_size: int | None  # bytes, cache-match key (None if backend lacks it)
    subject: str
    course_title: str
    chapter_title: str
    whisper_hint: str


@dataclass
class ClaimedJob:
    job_id: int
    episode_id: int
    language: str
    download_url: str
    download_header: dict[str, str]
    episode: EpisodeInfo


class ApiClient:
    def __init__(self, base_url: str, ingest_key: str, worker_id: str, timeout: float = 30.0):
        self._base = base_url.rstrip("/")
        self._key = ingest_key
        self._worker_id = worker_id
        self._timeout = timeout
        self._session = requests.Session()
        self._session.headers.update(
            {"X-Ingest-Key": ingest_key, "X-Worker-ID": worker_id}
        )

    def _post(self, path: str, json: dict | None = None) -> requests.Response:
        url = f"{self._base}{path}"
        return self._session.post(url, json=json, timeout=self._timeout)

    def claim(self) -> ClaimedJob | None:
        """Claim the next job. Returns None when the queue is empty."""
        resp = self._post("/api/v1/subtitle-jobs/claim")
        resp.raise_for_status()
        body = resp.json()
        job = body.get("job")
        if not job:
            return None
        ep = body.get("episode") or {}
        return ClaimedJob(
            job_id=job["id"],
            episode_id=job["episode_id"],
            language=job.get("language", "zh-CN"),
            download_url=body["download_url"],
            download_header=body.get("download_header") or {},
            episode=EpisodeInfo(
                title=ep.get("title", ""),
                duration_seconds=ep.get("duration_seconds"),
                filename=ep.get("filename", ""),
                file_size=ep.get("file_size"),
                subject=ep.get("subject", ""),
                course_title=ep.get("course_title", ""),
                chapter_title=ep.get("chapter_title", ""),
                whisper_hint=ep.get("whisper_hint", ""),
            ),
        )

    def complete(self, job_id: int, srt_content: str) -> None:
        resp = self._post(
            f"/api/v1/subtitle-jobs/{job_id}/complete",
            json={"srt_content": srt_content},
        )
        if resp.status_code == 409:
            raise StaleCompletion(f"job {job_id} no longer processing")
        resp.raise_for_status()

    def heartbeat(self, job_id: int, progress_ratio: float | None = None) -> None:
        body: dict = {}
        if progress_ratio is not None:
            body["progress_ratio"] = progress_ratio
        resp = self._post(f"/api/v1/subtitle-jobs/{job_id}/heartbeat", json=body or None)
        resp.raise_for_status()

    def fail(self, job_id: int, error: str) -> None:
        # Truncate so a panic stack doesn't blow past the server's text column.
        self._post(
            f"/api/v1/subtitle-jobs/{job_id}/fail",
            json={"error": error[:2000]},
        )
