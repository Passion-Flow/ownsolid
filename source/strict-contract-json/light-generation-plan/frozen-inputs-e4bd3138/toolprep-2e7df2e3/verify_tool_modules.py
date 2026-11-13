"""Verify pinned, already prepared Go module sources without invoking Go or writing files."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import sys


def _sha(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _h1(files):
    digest = hashlib.sha256()
    for name, path in sorted(files):
        if "\n" in name or path.is_symlink() or not path.is_file():
            raise ValueError("module checksum input must be a regular file with a safe name")
        digest.update(f"{_sha(path)}  {name}\n".encode())
    return "h1:" + base64.b64encode(digest.digest()).decode()


def _escape(value):
    return "".join("!" + character.lower() if character.isupper() else character for character in value)


def verify_cached_modules(cache, pins):
    """Check source h1 and canonical cached .mod h1, including pre-module repositories."""
    if not isinstance(pins, list) or not pins:
        raise ValueError("module pins must be a nonempty list")
    cache = Path(cache)
    results = []
    for pin in pins:
        name, version = pin["module"], pin["version"]
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._~+-]*(/[A-Za-z0-9][A-Za-z0-9._~+-]*)+", name):
            raise ValueError("unsupported pinned module path")
        if not re.fullmatch(r"v[0-9][A-Za-z0-9.+-]*", version):
            raise ValueError("unsupported pinned module version")
        root = cache / (_escape(name) + "@" + _escape(version))
        if root.is_symlink() or not root.is_dir():
            raise ValueError(f"{name}@{version}: prepared module source is missing or unsafe")
        paths = sorted(root.rglob("*"))
        if any(path.is_symlink() for path in paths):
            raise ValueError(f"{name}@{version}: symlink in prepared module source")
        source = _h1([(name + "@" + version + "/" + path.relative_to(root).as_posix(), path)
                      for path in paths if path.is_file()])
        if source != pin["sum"]:
            raise ValueError(f"{name}@{version}: source checksum differs from the pin")
        cached_mod = cache / "cache/download" / _escape(name) / "@v" / (_escape(version) + ".mod")
        if cached_mod.is_symlink() or not cached_mod.is_file():
            raise ValueError(f"{name}@{version}: canonical cached .mod is missing or unsafe")
        mod_sum = _h1([("go.mod", cached_mod)])
        if mod_sum != pin["go_mod_sum"]:
            raise ValueError(f"{name}@{version}: cached .mod checksum differs from the pin")
        source_mod = root / "go.mod"
        if source_mod.exists() and _sha(source_mod) != _sha(cached_mod):
            raise ValueError(f"{name}@{version}: source go.mod differs from canonical cached .mod")
        notices = {path.name: _sha(path) for path in root.iterdir() if path.is_file()
                   and path.name.lower().startswith(("license", "copying", "notice"))}
        results.append({**pin, "verified_source_h1": source, "verified_go_mod_h1": mod_sum,
                        "source_go_mod_present": source_mod.exists(), "cached_go_mod": str(cached_mod),
                        "notices_sha256": notices})
    return results


def main():
    """Check the prepared cache against a local preparation JSON's modules list."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cache-root", required=True, type=Path)
    parser.add_argument("--pins", required=True, type=Path)
    args = parser.parse_args()
    try:
        pins = json.loads(args.pins.read_text(encoding="utf-8"))["modules"]
        report = verify_cached_modules(args.cache_root, pins)
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(f"tool cache verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps({"modules": report}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
