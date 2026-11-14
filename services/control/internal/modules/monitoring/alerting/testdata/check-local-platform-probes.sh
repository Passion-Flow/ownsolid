#!/usr/bin/env bash
set -euo pipefail

for binary in "${OWNSOLID_TEST_BLACKBOX_EXPORTER:-}" "${OWNSOLID_TEST_VMAGENT:-}" \
    "${OWNSOLID_TEST_VICTORIA_METRICS:-}" "${OWNSOLID_TEST_VICTORIA_LOGS:-}"; do
    if [[ $binary != /* || ! -x $binary ]]; then
        echo "Set the Blackbox Exporter, vmagent, VictoriaMetrics and VictoriaLogs test binary paths." >&2
        exit 2
    fi
done
cd "$(dirname "$0")/../../../../../../.."
df -h /
free -m
ss -lntp
umask 077
work=$(mktemp -d)
cleanup() {
    if [[ -f $work/alerting.test ]]; then rm -- "$work/alerting.test"; fi
    rmdir -- "$work"
}
trap cleanup EXIT
export GOMAXPROCS=2
go -C services/control test -p 2 -tags alerting_integration -c \
    -o "$work/alerting.test" ./internal/modules/monitoring/alerting
cd services/control/internal/modules/monitoring/alerting
unshare --net --pid --fork bash -euc '
    ip link set lo up
    test "$(ip -o link show | wc -l)" -eq 1
    ip -brief address
    test -z "$(ip route show default)"
    test -z "$(ip -6 route show default)"
    exec "$1" -test.v -test.run "^TestPlatformProbesWriteScopedReadinessMetrics$" -test.timeout 90s
' bash "$work/alerting.test"
