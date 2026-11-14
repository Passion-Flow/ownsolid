#!/usr/bin/env bash
set -euo pipefail

# The reused sink listens on all interfaces and accepts any sender. Require a
# disposable namespace with only a loopback interface before starting it.
test "$(ip -o link show | wc -l)" -eq 1
ip link show lo >/dev/null
test -z "$(ss -Hlnt 'sport = :465 or sport = :59093')"
: "${OWNSOLID_TEST_ALERTMANAGER:?Set an absolute path to Alertmanager 0.34.2}"
case "$OWNSOLID_TEST_ALERTMANAGER" in
  /*) ;;
  *) echo "Alertmanager path must be absolute" >&2; exit 1 ;;
esac

cd "$(dirname "$0")/.."
umask 077
work=$(mktemp -d)
cleanup() {
  if [[ -n ${alertmanager_pid:-} ]]; then
    kill -TERM "$alertmanager_pid" 2>/dev/null || true
    wait "$alertmanager_pid" || true
  fi
  if [[ -n ${smtp_pid:-} ]]; then
    kill -TERM "$smtp_pid" 2>/dev/null || true
    wait "$smtp_pid" || true
  fi
  find "$work" -type f -delete
  find "$work" -depth -type d -empty -delete
}
trap cleanup EXIT

mkdir "$work/mail"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=127.0.0.1 \
  -addext subjectAltName=IP:127.0.0.1 -keyout "$work/tls.key" \
  -out "$work/tls.pem" >"$work/openssl.log" 2>&1
python3 -c '
import pathlib
import sys
work = pathlib.Path(sys.argv[1])
sample = pathlib.Path("alertmanager-smtp.example.yaml").read_text()
sample = sample.replace("smtp.site.example:465", "127.0.0.1:465")
sample = sample.replace("/etc/ownsolid/alertmanager/smtp-ca.pem", str(work / "tls.pem"))
(work / "alertmanager.yaml").write_text(sample)
' "$work"
python3 ../../../../../../tools/dev/smtp_sink.py "$work/mail" "$work/tls.pem" "$work/tls.key" >"$work/smtp.log" 2>&1 &
smtp_pid=$!
"$OWNSOLID_TEST_ALERTMANAGER" --config.file="$work/alertmanager.yaml" --storage.path="$work/state" \
  --cluster.listen-address= --web.listen-address=127.0.0.1:59093 >"$work/manager.log" 2>&1 &
alertmanager_pid=$!

wait_for_http() {
  local attempt
  for attempt in $(seq 1 40); do
    if curl --noproxy '*' -fsS --max-time 2 http://127.0.0.1:59093/-/ready >/dev/null 2>&1; then
      return
    fi
    kill -0 "$alertmanager_pid" "$smtp_pid" || break
    sleep 1
  done
  cat "$work/manager.log" "$work/smtp.log" >&2
  return 1
}
wait_for_http
curl --noproxy '*' -fsS --max-time 10 -H 'Content-Type: application/json' \
  -d '[{"labels":{"alertname":"OwnsolidLocalMailTest","site_id":"fixture-site","cell_id":"fixture-cell","severity":"critical"}}]' \
  http://127.0.0.1:59093/api/v2/alerts

for attempt in $(seq 1 60); do
  if python3 - "$work/mail" <<'PY'
from email import policy
from email.parser import BytesParser
from pathlib import Path
import sys

messages = sorted(Path(sys.argv[1]).glob("*.eml"))
if len(messages) != 1:
    sys.exit(1)
message = BytesParser(policy=policy.default).parsebytes(messages[0].read_bytes())
if message["To"] != "oncall@site.example" or "OwnsolidLocalMailTest" not in message["Subject"]:
    sys.exit(1)
PY
  then
    echo "Alertmanager delivered the alert to the local SMTP relay over verified TLS."
    exit 0
  fi
  sleep 1
done
cat "$work/manager.log" "$work/smtp.log" >&2
exit 1
