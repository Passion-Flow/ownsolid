"""Refuse the retired three-carrier audit entry without touching host resources."""
import sys

print("Retired audit entry: no lock or test was requested. The original bytes are "
      "preserved in run_go_wire.legacy-three-carrier.py. The one authorized native "
      "segment is consumed; a new source-qualified window is required.", file=sys.stderr)
raise SystemExit(78)
