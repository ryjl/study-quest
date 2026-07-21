"""Configuration loading for the subtitle worker.

Resolution order (later wins):
  1. config.yaml (structured defaults; the version-controlled example is
     config.example.yaml).
  2. environment variables — secrets (the ingest key) MUST come from here, and
     any scalar can be overridden. Env vars are prefixed ``SQ_`` and use
     ``__`` as a section separator, e.g. ``SQ_WHISPER__COMPUTE_TYPE``.
  3. CLI flags (see worker.py argparse) — temporary overrides for a single run.

``~`` and ``$VAR`` inside the YAML are expanded against the user's home and
environment, so cache dirs / model paths can be written naturally.
"""

from __future__ import annotations

import dataclasses
import os
from pathlib import Path
from typing import get_type_hints

import yaml

ENV_PREFIX = "SQ"
ENV_SEP = "__"


@dataclasses.dataclass
class BackendCfg:
    base_url: str = "http://localhost:8080"
    ingest_key: str = ""  # secret: pulled from SQ_INGEST_KEY, never from the YAML


@dataclasses.dataclass
class WorkerCfg:
    id: str = ""  # "" → socket.gethostname()
    poll_interval: float = 30.0  # seconds to sleep when the queue is empty
    heartbeat_interval: float = 30.0


@dataclasses.dataclass
class CacheCfg:
    dirs: list[str] = dataclasses.field(default_factory=list)


@dataclasses.dataclass
class AudioCfg:
    sample_rate: int = 16000
    channels: int = 1
    # Where extracted WAVs are cached (keyed by filename+size) so a retry
    # doesn't re-download from the netdisk. Default ~/.cache/sq-whisper/wav.
    wav_cache_dir: str = ""
    wav_cache_max_age_days: float = 7.0
    # Where final SRTs are cached (keyed by filename+size+model). This is the
    # fastest path: a previous whisper run already produced the SRT, so a retry
    # skips even the transcription. Default ~/.cache/sq-whisper/srt.
    # (Lives under AudioCfg to mirror wav_cache_dir — both are extracted/derived
    # artifacts cached on the worker.) TTL is fixed at 30 days in srt_cache.py.
    srt_cache_dir: str = ""


@dataclasses.dataclass
class WhisperCfg:
    model_path: str = ""
    device: str = "cuda"  # only cuda is supported — no CPU fallback (see README)
    compute_type: str = "int8_float16"  # misconfigure → error, no auto-downgrade
    language: str = "zh"
    vad_filter: bool = True
    beam_size: int = 5
    # Style引导基础句, prepended to the auto-built course context.
    base_prompt: str = "以下是普通话的句子。"


@dataclasses.dataclass
class Config:
    backend: BackendCfg = dataclasses.field(default_factory=BackendCfg)
    worker: WorkerCfg = dataclasses.field(default_factory=WorkerCfg)
    cache: CacheCfg = dataclasses.field(default_factory=CacheCfg)
    audio: AudioCfg = dataclasses.field(default_factory=AudioCfg)
    whisper: WhisperCfg = dataclasses.field(default_factory=WhisperCfg)


def _expand(s: str) -> str:
    """Expand ``~`` and ``$VAR`` for a string value."""
    return os.path.expandvars(os.path.expanduser(s))


def _coerce(raw: str, typ: type) -> bool | int | float | str:
    if typ is bool:
        return raw.lower() in ("1", "true", "yes", "on")
    if typ is int:
        return int(raw)
    if typ is float:
        return float(raw)
    return _expand(raw)


def _apply_yaml(cfg: Config, raw: dict) -> None:
    for section_field in dataclasses.fields(cfg):
        section_data = raw.get(section_field.name)
        if not isinstance(section_data, dict):
            continue
        section = getattr(cfg, section_field.name)
        hints = get_type_hints(type(section))
        for f in dataclasses.fields(section):
            if f.name not in section_data:
                continue
            v = section_data[f.name]
            if isinstance(v, str):
                v = _expand(v)
            # list fields (e.g. cache.dirs) pass through as-is.
            setattr(section, f.name, v)


def _apply_env(cfg: Config) -> None:
    """Apply ``SQ_SECTION__KEY`` env overrides onto the dataclass tree."""
    for section_field in dataclasses.fields(cfg):
        section = getattr(cfg, section_field.name)
        hints = get_type_hints(type(section))
        prefix = f"{ENV_PREFIX}_{section_field.name.upper()}{ENV_SEP}"
        for f in dataclasses.fields(section):
            env_name = prefix + f.name.upper()
            raw = os.environ.get(env_name)
            if raw is None:
                continue
            setattr(section, f.name, _coerce(raw, hints[f.name]))


def load(path: str | Path) -> Config:
    """Load config from a YAML file, then apply env overrides."""
    cfg = Config()
    p = Path(_expand(str(path)))
    if p.exists():
        with p.open("r", encoding="utf-8") as fh:
            raw = yaml.safe_load(fh) or {}
        _apply_yaml(cfg, raw)
    _apply_env(cfg)
    # The ingest key is always read from the environment (never trusted to the
    # YAML, which may get committed/shared).
    cfg.backend.ingest_key = os.environ.get("SQ_INGEST_KEY", cfg.backend.ingest_key)
    return cfg


def worker_id(cfg: Config) -> str:
    """Resolve the worker id, defaulting to the hostname when unset."""
    if cfg.worker.id:
        return cfg.worker.id
    import socket

    return socket.gethostname()
