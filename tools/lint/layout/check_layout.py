"""Enforce the repository layout and language rules defined in AGENTS.md.

The repository-specific allowlists live in rules.json next to this script, so the
same checker serves every ownsolid repository.

Usage: python3 tools/lint/layout/check_layout.py [repository-root]
Exits with status 1 and prints one line per violation when a rule is broken.
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Iterable, Optional

RULES_FILE = Path(__file__).resolve().parent / "rules.json"
LOWERCASE_SEGMENT = re.compile(r"^[a-z0-9][a-z0-9._-]*$")
DECISION_RECORD = re.compile(r"^\d{4}-[a-z0-9]+(?:-[a-z0-9]+)*\.md$")
DECISIONS_DIRECTORY = ("docs", "decisions")


@dataclass(frozen=True)
class Rules:
    root_files: frozenset
    top_directories: dict  # name -> frozenset of allowed subdirectories, or None for any
    top_directory_files: frozenset
    env_example: str
    conventional_names: frozenset
    hidden_names: frozenset
    exempt_directories: frozenset


def load_rules(path: Path = RULES_FILE) -> Rules:
    data = json.loads(path.read_text(encoding="utf-8"))
    return Rules(
        root_files=frozenset(data["root_files"]),
        top_directories={
            name: (None if children is None else frozenset(children))
            for name, children in data["top_directories"].items()
        },
        top_directory_files=frozenset(data["top_directory_files"]),
        env_example=data["env_example"],
        conventional_names=frozenset(data["conventional_names"]),
        hidden_names=frozenset(data["hidden_names"]),
        exempt_directories=frozenset(data["exempt_directories"]),
    )


def check_paths(paths: Iterable[str], read_text: Callable[[str], Optional[str]], rules: Rules) -> list:
    """Return one message per layout violation for the given repository-relative paths."""
    errors = []
    for path in sorted(set(paths)):
        parts = path.split("/")
        errors.extend(_check_placement(path, parts, rules))
        # Translated text and vendored third-party files keep their original names and language.
        if rules.exempt_directories.isdisjoint(parts):
            errors.extend(_check_names(path, parts, rules))
            errors.extend(_check_language(path, read_text(path)))
    return errors


def _check_placement(path: str, parts: list, rules: Rules) -> list:
    name = parts[-1]
    if len(parts) == 1:
        return [] if name in rules.root_files else [f"{path}: file not allowed at repository root"]
    top = parts[0]
    if top not in rules.top_directories:
        return [f"{path}: top-level directory '{top}' is not defined in AGENTS.md"]
    if name.startswith(".env") and path != rules.env_example:
        return [f"{path}: environment files belong only at {rules.env_example}"]
    allowed = rules.top_directories[top]
    if len(parts) == 2:
        if allowed is None or name in rules.top_directory_files:
            return []
        return [f"{path}: place files inside one of {top}/{{{','.join(sorted(allowed))}}}"]
    if allowed is not None and parts[1] not in allowed:
        return [f"{path}: '{top}/{parts[1]}' is not a defined subdirectory"]
    if tuple(parts[:2]) == DECISIONS_DIRECTORY and len(parts) == 3:
        if name != "README.md" and not DECISION_RECORD.match(name):
            return [f"{path}: decision records are named NNNN-kebab-title.md"]
    return []


def _check_names(path: str, parts: list, rules: Rules) -> list:
    for segment in parts:
        if segment in rules.conventional_names or segment in rules.hidden_names:
            continue
        if not LOWERCASE_SEGMENT.match(segment):
            return [f"{path}: path segment '{segment}' must be lowercase [a-z0-9._-]"]
    return []


def _check_language(path: str, text: Optional[str]) -> list:
    if text is None:
        return []
    for number, line in enumerate(text.splitlines(), start=1):
        for character in line:
            if ord(character) > 127 and unicodedata.category(character).startswith("L"):
                return [f"{path}:{number}: non-English letter '{character}' outside a locales/ directory"]
    return []


def _repository_files(root: Path) -> list:
    output = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
        check=True, capture_output=True,
    ).stdout
    return [item for item in output.decode("utf-8").split("\0") if item]


def _reader(root: Path) -> Callable[[str], Optional[str]]:
    def read(path: str) -> Optional[str]:
        target = root / path
        if not target.is_file():
            return None
        data = target.read_bytes()
        if b"\0" in data[:8192]:
            return None
        try:
            return data.decode("utf-8")
        except UnicodeDecodeError:
            return None
    return read


def main(argv: list) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    errors = check_paths(_repository_files(root), _reader(root), load_rules())
    for error in errors:
        print(error)
    if errors:
        print(f"layout check failed: {len(errors)} violation(s)")
        return 1
    print("layout check passed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
