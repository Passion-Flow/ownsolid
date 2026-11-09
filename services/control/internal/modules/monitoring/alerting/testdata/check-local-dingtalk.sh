#!/usr/bin/env bash
set -euo pipefail

for binary in "${OWNSOLID_TEST_ALERTMANAGER:-}"; do
    if [[ $binary != /* || ! -x $binary ]]; then
        echo "Set OWNSOLID_TEST_ALERTMANAGER to the verified absolute binary." >&2
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
    for name in dingtalk-relay alerting.test; do
        if [[ -f $work/$name ]]; then rm -- "$work/$name"; fi
    done
    rmdir -- "$work"
}
trap cleanup EXIT
export GOMAXPROCS=2
export OWNSOLID_TEST_DINGTALK_RELAY="$work/dingtalk-relay"
go -C services/control build -p 2 -o "$OWNSOLID_TEST_DINGTALK_RELAY"     ./internal/modules/monitoring/alerting/cmd/dingtalk-relay
go -C services/control test -p 2 -tags alerting_integration -c     -o "$work/alerting.test" ./internal/modules/monitoring/alerting
cd services/control/internal/modules/monitoring/alerting
unshare --net --pid --fork bash -euc '
    ip link set lo up
    test "$(ip -o link show | wc -l)" -eq 1
    ip -brief address
    test -z "$(ip route show default)"
    test -z "$(ip -6 route show default)"
    exec "$1" -test.v -test.run "^TestDingTalkRelayRetriesOneDurableTenantDelivery$" -test.timeout 90s
' bash "$work/alerting.test"
