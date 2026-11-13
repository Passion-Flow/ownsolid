#!/usr/bin/env python3
"""Refuse all admissions from retired Mac carrier epoch 50313339."""
from pathlib import Path
import sys
LOCK = Path("/private/tmp/ownsolid-workstation-full-gate.lock")
class Refused(RuntimeError):
    """The old Mac admission epoch is closed."""
def run(*args, **kwargs):
    raise Refused("OLD_MAC_EPOCH_CLOSED: no open, create, wait, fallback or workload is permitted")
if __name__ == "__main__":
    print("OLD_MAC_EPOCH_CLOSED", file=sys.stderr)
    raise SystemExit(75)
