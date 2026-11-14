#!/bin/sh
set -eu
root=$1
evidence=$2
provider=$(cat "$evidence/gitea-prototype/container-name")
name="iam118-live-pg-$$"
created=false
cleanup() { if [ "$created" = true ]; then docker rm -f -v "$name" >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT INT TERM
docker run --pull never --detach --rm --network "container:$provider" --name "$name" --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=iam118_live_test --tmpfs /var/lib/postgresql:rw,size=1g 'pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47' >/dev/null
created=true
ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
 if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
[ "$ready" = true ]
docker inspect "$provider" --format 'provider_network={{.HostConfig.NetworkMode}} image={{.Config.Image}}'
docker inspect "$name" --format 'postgres_network={{.HostConfig.NetworkMode}} image={{.Config.Image}}'
docker exec "$name" psql -U postgres -d iam118_live_test -c 'CREATE EXTENSION vector; SHOW server_version;'
docker exec "$name" mkdir -p /tmp/iam-live /tmp/tree/api/openapi/v1/identity /tmp/tree/api/openapi/v1/sso /tmp/tree/services/control/internal/modules/identity/http /tmp/tree/services/control/internal/modules/sso/testdata
docker cp "$evidence/identity-http-live.test" "$name:/tmp/identity-http-live.test"
docker exec "$name" chown postgres:postgres /tmp/identity-http-live.test
docker exec "$name" chmod 500 /tmp/identity-http-live.test
docker cp "$root/api/openapi/v1/identity/openapi.json" "$name:/tmp/tree/api/openapi/v1/identity/openapi.json"
docker cp "$root/api/openapi/v1/sso/openapi.json" "$name:/tmp/tree/api/openapi/v1/sso/openapi.json"
docker cp "$root/services/control/internal/modules/sso/testdata/connector_sign_in_tests.up.sql" "$name:/tmp/tree/services/control/internal/modules/sso/testdata/connector_sign_in_tests.up.sql"
for file in fixture.json server.pem user-password; do
 docker cp "$evidence/gitea-prototype/$file" "$name:/tmp/iam-live/$file"
done
docker exec "$name" chown -R postgres:postgres /tmp/iam-live
docker exec "$name" chmod 700 /tmp/iam-live
docker exec "$name" chmod 600 /tmp/iam-live/fixture.json /tmp/iam-live/server.pem /tmp/iam-live/user-password
docker exec "$name" chown -R postgres:postgres /tmp/tree
docker exec --user postgres "$name" sh -c 'test -x /tmp/identity-http-live.test && test -r /tmp/tree/services/control/internal/modules/sso/testdata/connector_sign_in_tests.up.sql && test -r /tmp/tree/api/openapi/v1/identity/openapi.json && test -r /tmp/tree/api/openapi/v1/sso/openapi.json'
docker exec --user postgres --workdir /tmp/tree/services/control/internal/modules/identity/http --env 'PROJECT_ONE_TEST_DSN=postgres://postgres@127.0.0.1:5432/iam118_live_test?sslmode=disable' --env OWNSOLID_IAM_GITEA_FIXTURE_FILE=/tmp/iam-live/fixture.json "$name" sh -c 'umask 077; exec /tmp/identity-http-live.test -test.v -test.timeout=180s -test.run "^(TestConnectorSignInTestActualGiteaHTTPS|TestSSOActualGiteaRecoveryAndDeviceRevocationHTTPS)$" -test.count=1'
