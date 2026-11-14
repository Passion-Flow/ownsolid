#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
    feishu) title=Feishu; variable=OWNSOLID_TEST_FEISHU_RELAY ;;
    dingtalk) title=DingTalk; variable=OWNSOLID_TEST_DINGTALK_RELAY ;;
    wecom) title=WeCom; variable=OWNSOLID_TEST_WECOM_RELAY ;;
    *) echo "Select feishu, dingtalk or wecom." >&2; exit 2 ;;
esac
binary="$1-relay"
alertmanager="${OWNSOLID_TEST_ALERTMANAGER:-}"
if [[ $alertmanager != /* || ! -x $alertmanager ]]; then
    echo "Set OWNSOLID_TEST_ALERTMANAGER to the verified absolute binary." >&2
    exit 2
fi
cd "$(dirname "$0")/../../../../../../.."
df -h /
free -m
ss -lntp
umask 077
work=$(mktemp -d)
cleanup() {
    for name in "$binary" alerting.test; do
        if [[ -f $work/$name ]]; then rm -- "$work/$name"; fi
    done
    rmdir -- "$work"
}
trap cleanup EXIT
export GOMAXPROCS=2
export "$variable=$work/$binary"
go -C services/control build -p 2 -o "$work/$binary" \
    "./internal/modules/monitoring/alerting/cmd/$binary"
go -C services/control test -p 2 -tags alerting_integration -c \
    -o "$work/alerting.test" ./internal/modules/monitoring/alerting
cd services/control/internal/modules/monitoring/alerting
unshare --net --pid --fork bash -euc '
    ip link set lo up
    test "$(ip -o link show | wc -l)" -eq 1
    ip -brief address
    test -z "$(ip route show default)"
    test -z "$(ip -6 route show default)"
    exec "$1" -test.v -test.run "$2" -test.timeout 90s
' bash "$work/alerting.test" "^Test${title}RelayRetriesOneDurableTenantDelivery$"
