#!/usr/bin/env bash
set -euo pipefail

for binary in "${OWNSOLID_TEST_VICTORIA_LOGS:-}" "${OWNSOLID_TEST_VMALERT:-}" "${OWNSOLID_TEST_ALERTMANAGER:-}"; do
    if [[ $binary != /* || ! -x $binary ]]; then
        echo "Set OWNSOLID_TEST_VICTORIA_LOGS, OWNSOLID_TEST_VMALERT and OWNSOLID_TEST_ALERTMANAGER to the verified absolute binaries." >&2
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
    exec "$1" -test.v -test.run "^TestNodeJournalRule(DeliversAndResolvesByWorkspace|TenThousandNodes)$" -test.timeout 145s
' bash "$work/alerting.test"
