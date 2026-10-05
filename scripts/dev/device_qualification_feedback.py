#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import shlex
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

_VERBOSE = os.environ.get("RNEXUS_QUALIFICATION_VERBOSE", "").strip().lower() in {"1", "true", "yes", "on"}


def set_verbose(value: bool) -> None:
    global _VERBOSE
    _VERBOSE = bool(value)


def verbose_enabled() -> bool:
    return _VERBOSE


def _emit(text: str = "") -> None:
    print(text, file=sys.stderr, flush=True)


def phase(title: str, why: str = "") -> None:
    _emit(f"\n== {title} ==")
    if why:
        _emit(f"   Why: {why}")


def step(scope: str, name: str, why: str = "") -> None:
    _emit(f"→ {scope}: {name}")
    if why:
        _emit(f"  Why: {why}")


def ok(scope: str, message: str, detail: str = "") -> None:
    _emit(f"✓ {scope}: {message}")
    if detail:
        _emit(f"  {detail}")


def reuse(scope: str, evidence: str | Path, reason: str) -> None:
    _emit(f"↻ {scope}: reusing current private evidence")
    _emit(f"  Why: {reason}")
    _emit(f"  Evidence: {evidence}")


def note(message: str) -> None:
    _emit(f"  {message}")


def debug(label: str, value: Any) -> None:
    if not _VERBOSE:
        return
    if isinstance(value, (dict, list)):
        rendered = json.dumps(value, indent=2, sort_keys=True, ensure_ascii=False)
    else:
        rendered = str(value)
    _emit(f"  Debug {label}:")
    for line in rendered.splitlines() or [""]:
        _emit(f"    {line}")


def command(argv: Iterable[str]) -> None:
    if _VERBOSE:
        _emit(f"  Command: {shlex.join(list(argv))}")


def summarize_value(value: Any, limit: int = 1800) -> str:
    if isinstance(value, dict):
        rendered = json.dumps(value, sort_keys=True, ensure_ascii=False)
    elif isinstance(value, list):
        rendered = json.dumps(value, ensure_ascii=False)
    else:
        rendered = repr(value)
    if len(rendered) > limit:
        return rendered[:limit] + "…"
    return rendered


@dataclass
class QualificationFailure(RuntimeError):
    summary: str
    why: str = ""
    expected: str = ""
    observed: str = ""
    command_text: str = ""
    evidence: str = ""
    promise: str = ""
    next_action: str = ""

    def __str__(self) -> str:
        return self.summary


def render_failure(exc: BaseException, scope: str = "RUNTIME-GRAND-G1-A") -> None:
    _emit(f"\n✗ {scope}: qualification failed")
    if isinstance(exc, QualificationFailure):
        _emit(f"  Problem: {exc.summary}")
        if exc.promise:
            _emit(f"  Blocks: {exc.promise}")
        if exc.why:
            _emit(f"  Why this matters: {exc.why}")
        if exc.expected:
            _emit(f"  Expected: {exc.expected}")
        if exc.observed:
            _emit(f"  Observed: {exc.observed}")
        if exc.command_text:
            _emit(f"  Command: {exc.command_text}")
        if exc.evidence:
            _emit(f"  Evidence: {exc.evidence}")
        if exc.next_action:
            _emit(f"  Next: {exc.next_action}")
    else:
        _emit(f"  Problem: {exc}")
        _emit("  Hint: rerun with --verbose for command/observation diagnostics when available.")
