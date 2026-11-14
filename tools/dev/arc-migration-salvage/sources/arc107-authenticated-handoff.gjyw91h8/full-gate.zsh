#!/bin/sh
printf "%s\n" "ARC_LEGACY_ADMISSION_CLOSED: no carrier, fallback, retry or queue is permitted." >&2
exit 78
