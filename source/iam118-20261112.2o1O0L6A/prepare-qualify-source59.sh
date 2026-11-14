#!/bin/sh
set -eu
root=$1
evidence=$2
cd "$root"
git add -- docs/research/sso-interactive-connector-tests.md services/control/internal/modules/identity/http/sso_connector_sign_in_test_integration_test.go
git diff --cached --check
git commit -m 'IAM-118: Strengthen known-tenant isolation and record gate boundaries' -m 'Preserve passing check/build and browser evidence and the exact delivery fixture failure under umask 077. Verify formatting and layout; keep the strengthened schema 59 native assertions and full product admission explicitly pending.'
git fetch origin
git rebase origin/main
python3 "$evidence/qualify-source59.py" "$root" "$evidence"
