#!/bin/sh
# Run against the disposable Docker daemon before any Compose service is created.
set -eu
umask 077
project=$1
out=$2
case "$project" in *[!a-z0-9-]*|'') exit 2;; esac
mkdir -m 0700 "$out"
since=$(date +%s)
docker events --since "$since" --filter type=container --filter "label=com.docker.compose.project=$project" \
    --filter event=create --filter event=start --filter event=die --filter event=oom \
    --filter event=restart --filter event=stop --filter event=kill \
    --format '{{.TimeNano}} {{.Action}} {{.Actor.ID}} {{index .Actor.Attributes "com.docker.compose.service"}} {{index .Actor.Attributes "exitCode"}}' > "$out/events.log" &
events=$!
inspect() {
    docker inspect --format '{{.Id}} {{.State.Pid}} {{.State.Status}} {{.State.ExitCode}} {{.State.OOMKilled}} {{.State.StartedAt}} {{.State.FinishedAt}} {{.RestartCount}}' "$1"
}
stop_child() {
    if jobs -p | grep -Fxq "$1"; then kill -TERM "$1" 2>/dev/null || true; fi
    wait "$1" 2>/dev/null || true
}
cleanup() {
    trap - 0
    set +e
    stop_child "$events"
    for marker in "$out"/*.follow; do
        test -f "$marker" || continue
        read -r id pid service < "$marker"
        stop_child "$pid"
        inspect "$id" > "$out/$service-$id.final-state" 2>/dev/null
        docker logs --timestamps "$id" > "$out/$service-$id.final.log" 2>/dev/null
    done
}
trap cleanup 0
trap 'exit 0' INT TERM
while kill -0 "$events" 2>/dev/null; do
    docker ps -a --no-trunc --filter "label=com.docker.compose.project=$project" \
        --format '{{.ID}} {{.Label "com.docker.compose.service"}}' > "$out/.containers"
    while read -r id service; do
        case "$service" in control|postgres) ;; *) continue;; esac
        case "$id" in *[!a-f0-9]*|'') continue;; esac
        test "${#id}" = 64 || continue
        marker="$out/$id.follow"
        if ! test -e "$marker"; then
            docker logs -f --timestamps "$id" > "$out/$service-$id.live.log" 2>&1 &
            printf '%s %s %s\n' "$id" "$!" "$service" > "$marker"
            inspect "$id" > "$out/$service-$id.initial-state" 2>/dev/null || true
        fi
    done < "$out/.containers"
    sleep 1
done
exit 1
