set +e
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/distribution "$name" /fixture/distribution.test -test.v -test.timeout 2m -test.run '^TestFormalAliasDescriptionRollbackRejectsStaleSnapshot$' > "$work/stale-snapshot-red.log" 2>&1
red=$?
set -e
cat "$work/stale-snapshot-red.log"
if [[ $red == 1 ]] && grep -F 'stale snapshot allowed the down script to drop newly committed description state' "$work/stale-snapshot-red.log" >/dev/null; then
 printf 'Confirmed stale-snapshot downgrade refusal gap\n' | tee "$work/stale-snapshot-red-confirmed"
else
 printf 'Stale-snapshot result requires inspection: %s\n' "$red"; exit 1
fi
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -w /repo/services/control/internal/modules/distribution "$name" /fixture/distribution.test -test.v -test.timeout 2m -test.run '^TestFormalAliasDescriptionRollbackIsolationPrototype$' | tee "$work/stale-snapshot-prototype.log"
exit 1
