# Historical ARC admission is closed until a new coordinator epoch is explicitly installed.
import sys
sys.stderr.write("ARC_LEGACY_ADMISSION_CLOSED: no carrier, fallback, retry or queue is permitted.\n")
raise SystemExit(78)
