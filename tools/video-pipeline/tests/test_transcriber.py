"""Tests for the Whisper initial_prompt builder."""

from __future__ import annotations

import transcriber
from api_client import EpisodeInfo
from transcriber import PROMPT_MAX_CHARS


def _ep(**kw) -> EpisodeInfo:
    defaults = dict(
        title="", duration_seconds=None, filename="", file_size=None,
        subject="", course_title="", chapter_title="", whisper_hint="",
    )
    defaults.update(kw)
    return EpisodeInfo(**defaults)


def test_all_three_parts_composed():
    ep = _ep(
        subject="math", course_title="高等数学", chapter_title="第一章 极限",
        whisper_hint="注意 ε-δ 定义",
    )
    p = transcriber.build_prompt(ep, "以下是普通话的句子。")
    assert p.startswith("以下是普通话的句子。")
    assert "数学" in p or "math" in p
    assert "高等数学" in p
    assert "第一章 极限" in p
    assert "注意 ε-δ 定义" in p


def test_missing_context_still_builds():
    ep = _ep(whisper_hint="只有 hint")
    p = transcriber.build_prompt(ep, "以下是普通话的句子。")
    assert p == "以下是普通话的句子。只有 hint"


def test_empty_everything():
    p = transcriber.build_prompt(_ep(), "")
    assert p == ""


def test_truncation_enforced():
    ep = _ep(course_title="课" * 1000)
    p = transcriber.build_prompt(ep, "")
    assert len(p) <= PROMPT_MAX_CHARS


def test_hint_is_preserved_when_short():
    """A short hint should survive intact even with context present."""
    ep = _ep(subject="math", course_title="数学", whisper_hint="重点听导数")
    p = transcriber.build_prompt(ep, "基础。")
    assert "重点听导数" in p


def test_srt_ts_format():
    """Sanity-check the SRT timestamp formatter."""
    assert transcriber._srt_ts(0) == "00:00:00,000"
    assert transcriber._srt_ts(1.5) == "00:00:01,500"
    assert transcriber._srt_ts(3661.25) == "01:01:01,250"
    assert transcriber._srt_ts(-5) == "00:00:00,000"  # clamped
