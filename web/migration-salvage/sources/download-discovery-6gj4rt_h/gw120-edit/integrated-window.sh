#!/bin/bash
set -euo pipefail
umask 077
root="$1"
lane="/Users/passion./Documents/Passion's WorkSpace/Development/lane-web"
cd "$lane"
target="$(mktemp -d "$root/final-target-XXXXXXXX")"
printf '%s\n' "$target" > "$root/current-final-target"
bash "$root/post-dialog-window.sh" "$target"
git diff --check
git add docs/research/gateway-model-description.md web/apps/console/e2e/alias-description.spec.ts web/apps/console/e2e/pages.ts web/apps/console/src/alias-description-dialog.tsx web/apps/console/src/deployment-gateway.tsx web/apps/portal/e2e/pages.ts web/packages/api/src/distribution.ts web/packages/api/src/index.ts web/packages/i18n/locales/en.json web/packages/i18n/locales/zh-cn.json web/packages/shell/testing/gateway.ts web/packages/ui/src/components/components.test.tsx web/packages/ui/src/components/toast.tsx web/packages/ui/src/components/dialog.tsx
git commit -m "GW-120: Reconcile Undo outcomes and protect description drafts" -m "Read the selected alias directly for revision review. Keep unavailable key issuance visible with its reason, scope actions to each published name, and prevent backdrop clicks from discarding an editor draft." -m "Verified shared UI unit tests and focused Chromium coverage for revision review, Unicode input, Undo replay/refusal, backdrop protection, both themes, keyboard focus and catalog text. Keep installed acceptance open."
git rev-parse HEAD > "$target/source"
git branch archive/web-description-20261113
git -c http.proxy=http://127.0.0.1:1082 -c http.version=HTTP/1.1 -c http.lowSpeedLimit=100 -c http.lowSpeedTime=15 fetch origin
git rebase origin/main
full="$(mktemp -d "$root/full-final-XXXXXXXX")"
cp "$root/full-99dnixnp/workers.mk" "$full/workers.mk"
printf '%s\n' "$full" > "$root/current-final-full"
bash "$root/full-window-v2.sh" "$full"
printf '\nREVIEW_WINDOW_READY %s\n' "$full"
if ! read -r -t 55 continuation; then
  printf 'Inspection window ended; no publication attempted.\n'
  exit 0
fi
if test "$continuation" != publish; then
  printf 'Inspection closed; no publication attempted.\n'
  exit 0
fi
test -z "$(git status --porcelain)"
qualified="$(cat "$full/source")"
git diff --exit-code "$qualified" HEAD -- web/
git -c http.proxy=http://127.0.0.1:1082 -c http.version=HTTP/1.1 -c http.lowSpeedLimit=100 -c http.lowSpeedTime=15 fetch origin
if ! git merge-base --is-ancestor origin/main HEAD; then
  printf 'MAIN_MOVED: rebase and affected checks required; no publication attempted.\n'
  exit 1
fi
git -c http.proxy=http://127.0.0.1:1082 -c http.version=HTTP/1.1 -c http.lowSpeedLimit=100 -c http.lowSpeedTime=15 push origin HEAD:main
git rev-parse HEAD
printf 'PUBLICATION_COMPLETE\n'
