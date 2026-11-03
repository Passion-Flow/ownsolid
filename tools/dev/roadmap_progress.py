"""Recount roadmap items and rewrite the progress table in docs/product/roadmap/index.md.

Usage: python3 tools/dev/roadmap_progress.py [--check]
With --check, exit 1 if the table is stale or item IDs are duplicated or malformed.
"""
import re
import sys
from pathlib import Path

ROADMAP = Path(__file__).resolve().parents[2] / "docs" / "product" / "roadmap"
INDEX = ROADMAP / "index.md"
PHASES = ["P0", "P1", "P2", "P3", "P4"]
ITEM = re.compile(r"^- \[( |x)\] ([A-Z]{2,4}-\d{3}) .*\((P[0-4])[^)]*\)")
ROW = re.compile(r"^\| (\d+) \| ([^|]+) \| \[([^\]]+)\]\(([^)]+)\) \|")


def count(path):
    totals = {phase: [0, 0] for phase in PHASES}
    ids = []
    for line in path.read_text(encoding="utf-8").splitlines():
        match = ITEM.match(line)
        if line.startswith("- [") and not match:
            raise SystemExit(f"{path.name}: malformed item: {line}")
        if match:
            done, item_id, phase = match.groups()
            ids.append(item_id)
            totals[phase][1] += 1
            totals[phase][0] += done == "x"
    return totals, ids


def main():
    check = "--check" in sys.argv
    lines = INDEX.read_text(encoding="utf-8").splitlines()
    seen, rebuilt = {}, []
    for line in lines:
        match = ROW.match(line)
        if not match:
            rebuilt.append(line)
            continue
        number, module, filename, target = match.groups()
        totals, ids = count(ROADMAP / target)
        for item_id in ids:
            if item_id in seen:
                raise SystemExit(f"duplicate item {item_id} in {target} and {seen[item_id]}")
            seen[item_id] = target
        cells = [f"{done}/{total}" if total else "-" for done, total in (totals[p] for p in PHASES)]
        rebuilt.append(f"| {number} | {module.strip()} | [{filename}]({target}) | " + " | ".join(cells) + " |")
    text = "\n".join(rebuilt) + "\n"
    if check:
        if text != INDEX.read_text(encoding="utf-8"):
            raise SystemExit("roadmap progress table is stale; run tools/dev/roadmap_progress.py")
        print(f"roadmap ok: {len(seen)} items")
        return
    INDEX.write_text(text, encoding="utf-8")
    print(f"progress table updated: {len(seen)} items")


main()
