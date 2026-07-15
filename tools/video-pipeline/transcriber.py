"""faster-whisper transcription wrapper.

Two responsibilities:
  1. Build the Whisper ``initial_prompt`` from course context. Whisper's prompt
     is NOT a free-form instruction — it's decoder context (~244 token budget)
     that biases style and injects hot-words. We compose three parts:
       - base_prompt  : style引导 (from config, e.g. "以下是普通话的句子。")
       - course ctx   : subject/course/chapter titles → subject terminology
       - ai_hint      : admin-authored针对性提示 (accent, the key theorem...)
     All auto-derived from the claim response, so every job gets a tailored
     prompt with zero manual per-video work.
  2. Lazily load the model and transcribe, reporting progress via a callback
     (faster-whisper yields segments lazily, so we know how far we've gotten).

GPU policy (hard requirement, NO fallback): the model loads on ``device=cuda``
with the configured ``compute_type``. If CUDA isn't available, the configured
device is wrong, or VRAM is insufficient, we raise — the worker exits and a
human intervenes. Silently degrading to CPU would leave a job pretending to
make progress while taking hours; that's worse than failing loud.
"""

from __future__ import annotations

import logging
from pathlib import Path

import config as cfg_mod
from api_client import EpisodeInfo

log = logging.getLogger("sq.transcriber")

# Whisper's decoder prompt budget. Keep some headroom under the hard limit so
# the course context + ai_hint don't crowd out the actual decoding.
PROMPT_MAX_CHARS = 240


def build_prompt(ep: EpisodeInfo, base_prompt: str) -> str:
    """Compose the initial_prompt: base style句 + course context + ai_hint.

    Truncated to PROMPT_MAX_CHARS (approximating the Whisper ~244-token budget
    in characters, which is conservative for CJK where 1 char ≈ 1 token).
    """
    parts: list[str] = []
    if base_prompt:
        parts.append(base_prompt)
    ctx_bits: list[str] = []
    if ep.subject:
        ctx_bits.append(f"{ep.subject}课")
    if ep.course_title:
        ctx_bits.append(ep.course_title)
    if ep.chapter_title:
        ctx_bits.append(ep.chapter_title)
    if ctx_bits:
        parts.append("，".join(ctx_bits) + "。")
    if ep.ai_hint:
        parts.append(ep.ai_hint)
    prompt = "".join(parts)
    return prompt[:PROMPT_MAX_CHARS]


class Transcriber:
    def __init__(self, whisper_cfg: cfg_mod.WhisperCfg):
        self._cfg = whisper_cfg
        self._model = None  # lazy: don't touch VRAM until the first real job

    def _ensure_model(self):
        if self._model is not None:
            return
        # GPU precondition check — fail loud, do NOT fall back to CPU.
        import ctranslate2

        n = ctranslate2.get_cuda_device_count()
        if n <= 0:
            raise RuntimeError(
                "no CUDA device visible to ctranslate2 (count=%d). This worker "
                "requires a GPU — aborting instead of falling back to CPU." % n
            )
        if self._cfg.device != "cuda":
            raise RuntimeError(
                f"device is configured as {self._cfg.device!r}; this worker only "
                "supports 'cuda' (no fallback). Fix the config."
            )
        from faster_whisper import WhisperModel

        log.info(
            "loading whisper model: %s (device=%s, compute_type=%s) — first job, ~10s",
            self._cfg.model_path, self._cfg.device, self._cfg.compute_type,
        )
        self._model = WhisperModel(
            self._cfg.model_path,
            device=self._cfg.device,
            compute_type=self._cfg.compute_type,
        )

    def transcribe(self, wav_path: str, initial_prompt: str, on_progress=None) -> str:
        """Transcribe a WAV → SRT string.

        on_progress(ratio: float) is called as each segment completes, with the
        ratio in [0, 1] of total audio duration. May be None.
        """
        self._ensure_model()
        assert self._model is not None
        log.info("transcribing %s (prompt=%d chars)", Path(wav_path).name, len(initial_prompt))

        segments, info = self._model.transcribe(
            wav_path,
            language=self._cfg.language,
            vad_filter=self._cfg.vad_filter,
            beam_size=self._cfg.beam_size,
            initial_prompt=initial_prompt or None,
        )
        total = info.duration or 0.0

        lines: list[str] = []
        idx = 0
        for seg in segments:  # lazy generator — transcribe streams as it goes
            idx += 1
            lines.append(_format_srt_block(idx, seg.start, seg.end, seg.text))
            if on_progress and total > 0:
                try:
                    on_progress(min(seg.end / total, 1.0))
                except Exception:
                    log.debug("progress callback raised, ignoring", exc_info=True)
        return "".join(lines)


def _format_srt_block(index: int, start: float, end: float, text: str) -> str:
    return f"{index}\n{_srt_ts(start)} --> {_srt_ts(end)}\n{text.strip()}\n\n"


def _srt_ts(t: float) -> str:
    """Seconds → 'HH:MM:SS,mmm' (SRT uses a comma before the millis)."""
    if t < 0:
        t = 0
    ms = int(round((t - int(t)) * 1000))
    h = int(t // 3600)
    m = int((t % 3600) // 60)
    s = int(t % 60)
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"
