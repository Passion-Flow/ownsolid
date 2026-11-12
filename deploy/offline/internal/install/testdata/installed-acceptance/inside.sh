#!/bin/sh
# The evidence bind receives packets before any software under test starts.
set -eu
umask 077
: "${ACCEPTANCE_RUN_ID:?}" "${ACCEPTANCE_STARTUP_SECONDS:?}" "${ACCEPTANCE_STOP_SECONDS:?}" "${ACCEPTANCE_SUITE_SECONDS:?}" "${ACCEPTANCE_WITH_HISTORY:?}" "${COMBINED_SOURCE_SHA:?}"
recorder=
daemon=
suite=
cleanup(){
 result=$?
 trap - EXIT TERM INT
 if [ -n "$suite" ]; then kill "$suite" 2>/dev/null || true; wait "$suite" 2>/dev/null || true; fi
 stopped=false
 if [ -n "$daemon" ] && docker info >/dev/null 2>&1; then
  containers=$(docker ps --quiet)
  if [ -n "$containers" ]; then docker stop --timeout "$ACCEPTANCE_STOP_SECONDS" $containers >/evidence/workload-stop.log 2>&1 || true; fi
  if [ -z "$(docker ps --quiet)" ]; then stopped=true; fi
 fi
 if [ -n "$daemon" ]; then kill "$daemon" 2>/dev/null || true; wait "$daemon" 2>/dev/null || true; fi
 recorder_result=1
 if [ -n "$recorder" ]; then
  kill "$recorder" 2>/dev/null || true
  recorder_result=0
  wait "$recorder" || recorder_result=$?
 fi
 if [ -f /evidence/egress-live.jsonl ]; then
  bytes=$(wc -c < /evidence/egress-live.jsonl)
  digest=$(sha256sum /evidence/egress-live.jsonl | cut -d ' ' -f 1)
  printf '{"run_id":"%s","recorder_exit":%s,"workloads_stopped":%s,"bytes":%s,"sha256":"%s"}\n' "$ACCEPTANCE_RUN_ID" "$recorder_result" "$stopped" "$bytes" "$digest" > /evidence/capture-ended.json.stage
  chmod 600 /evidence/capture-ended.json.stage
  mv /evidence/capture-ended.json.stage /evidence/capture-ended.json
 fi
 exit "$result"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
/bundle/egress-recorder -interface egress0 -log /evidence/egress-live.jsonl &
recorder=$!
for n in $(seq 1 50); do
 if ip link show egress0 >/dev/null 2>&1; then break; fi
 sleep .1
done
ip link set lo up
ip addr add 10.77.0.1/32 dev lo
ip link set egress0 up
ip addr add 198.18.0.1/32 dev egress0
ip -6 addr add 2001:db8:e9e5::1/128 dev egress0
sleep 2
ip route add default dev egress0
ip -6 route add default dev egress0
printf 'nameserver 192.0.2.53\n' > /etc/resolv.conf
test "$(ls -1 /sys/class/net | wc -l)" = 2
ip addr show
ip route show
ip -6 route show
/bundle/probe
sleep .2
test "$(wc -l < /evidence/egress-live.jsonl)" = 3
dockerd --host unix:///var/run/docker.sock --log-level error > /evidence/dockerd.log 2>&1 &
daemon=$!
for n in $(seq 1 "$ACCEPTANCE_STARTUP_SECONDS"); do
 if docker info >/dev/null 2>&1; then break; fi
 sleep 1
done
docker info >/dev/null
docker load -i /bundle/images.tar >/dev/null
docker network create lane-del-negative-control >/dev/null
docker run --rm --pull never --network lane-del-negative-control \
 --dns 192.0.2.53 --volume /bundle/probe:/probe:ro \
 --entrypoint /probe "$CONTROL_IMAGE"
docker network rm lane-del-negative-control >/dev/null
sleep .2
test "$(wc -l < /evidence/egress-live.jsonl)" = 5
cp /evidence/egress-live.jsonl /evidence/egress-baseline.jsonl
chmod 600 /evidence/egress-live.jsonl /evidence/egress-baseline.jsonl
case "$ACCEPTANCE_WITH_HISTORY" in
 true)
  HISTORY_METRICS_IMAGE="$METRICS_IMAGE" HISTORY_EVALUATOR_IMAGE="$EVALUATOR_IMAGE" \
   /bundle/history-prepare.test -test.run '^TestTemporaryCombinedHistoryPreparation$' -test.v -test.timeout "${ACCEPTANCE_STARTUP_SECONDS}s"
  /bundle/ownsolid-install fleet-history apply --config /evidence/history-inputs/site.yaml > /evidence/history-ready.json
  cat /evidence/history-ready.json
 ;;
 false) ;;
 *) echo 'Invalid history selection' >&2; exit 1 ;;
esac
export OWNSOLID_E2E_INSTALLER=/bundle/ownsolid-install
export OWNSOLID_E2E_COMPOSE_FILE=/bundle/compose.yaml
export OWNSOLID_E2E_IMAGE_ARCHIVE=/bundle/images.tar
export OWNSOLID_E2E_CONTROL_IMAGE="$CONTROL_IMAGE"
export OWNSOLID_E2E_POSTGRES_IMAGE="$POSTGRES_IMAGE"
export OWNSOLID_E2E_METRICS_IMAGE="$METRICS_IMAGE"
export OWNSOLID_E2E_LOGS_IMAGE="$LOGS_IMAGE"
export OWNSOLID_E2E_ROOT=/srv/ownsolid
export OWNSOLID_E2E_EGRESS_LOG=/evidence/egress-live.jsonl
export OWNSOLID_E2E_EGRESS_INTERFACE=egress0
/bundle/installed.test -test.run '^TestTemporaryCombinedInstalledAcceptance$' -test.v -test.timeout "${ACCEPTANCE_SUITE_SECONDS}s" &
suite=$!
suite_result=0
wait "$suite" || suite_result=$?
suite=
if [ "$suite_result" -ne 0 ]; then exit "$suite_result"; fi
test "$(wc -l < /evidence/egress-live.jsonl)" = 5
test "$(grep -Fc '"destination":"192.0.2.10:9"' /evidence/egress-live.jsonl)" = 2
test "$(grep -Fc '"destination":"[2001:db8::10]:9"' /evidence/egress-live.jsonl)" = 1
test "$(grep -Fc '"destination":"192.0.2.53:53"' /evidence/egress-live.jsonl)" = 2
test "$(grep -Fc '"dns_question":"probe.invalid. A"' /evidence/egress-live.jsonl)" = 2
kill -0 "$recorder"
echo 'COMBINED_INSTALLED_ZERO_EGRESS_PASSED: only deliberate host/workload negative controls captured'
