set -eu
umask 077
mkdir /tmp/fixture/db /tmp/fixture/artifacts
dpkg -i /tmp/libssl-dev.deb
cp /usr/share/doc/libssl-dev/copyright /tmp/fixture/artifacts/libssl-dev-copyright
dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' > /tmp/fixture/artifacts/packages.tsv
cd /tmp
tar xzf openldap-2.6.16.tgz
cd /tmp/openldap-2.6.16
./configure --prefix=/tmp/fixture/openldap --disable-backends --enable-mdb --disable-overlays --without-cyrus-sasl --with-tls=openssl --disable-shared --disable-modules --disable-balancer --disable-syslog CFLAGS=-O2 > /tmp/fixture/artifacts/configure.log 2>&1
make depend > /tmp/fixture/artifacts/depend.log 2>&1
for component in include libraries clients servers; do
 make -j2 -C "$component" >> /tmp/fixture/artifacts/make.log 2>&1
done
for component in include libraries clients servers; do
 make -C "$component" install >> /tmp/fixture/artifacts/install.log 2>&1
done
cp config.log /tmp/fixture/artifacts/config.log
/tmp/fixture/openldap/libexec/slapd -VV > /tmp/fixture/artifacts/version.log 2>&1
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -config /tmp/fixture/ca.cnf -keyout /tmp/fixture/ca-key.pem -out /tmp/fixture/ca.pem > /tmp/fixture/artifacts/ca-generation.log 2>&1
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -config /tmp/fixture/server.cnf -keyout /tmp/fixture/server-key.pem -out /tmp/fixture/server.csr > /tmp/fixture/artifacts/leaf-generation.log 2>&1
openssl x509 -req -in /tmp/fixture/server.csr -CA /tmp/fixture/ca.pem -CAkey /tmp/fixture/ca-key.pem -set_serial 2 -days 2 -extfile /tmp/fixture/server.cnf -extensions server -out /tmp/fixture/server.pem > /tmp/fixture/artifacts/leaf-signing.log 2>&1
/tmp/fixture/openldap/sbin/slapadd -f /tmp/fixture/slapd.conf -l /tmp/fixture/seed.ldif > /tmp/fixture/artifacts/seed.log 2>&1
/tmp/fixture/openldap/libexec/slapd -f /tmp/fixture/slapd.conf -h ldaps://127.0.0.1:1636 -d 0 > /tmp/fixture/artifacts/server.log 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true' EXIT HUP INT TERM
ready=no
for attempt in $(seq 1 40); do
 if LDAPTLS_CACERT=/tmp/fixture/ca.pem LDAPTLS_REQCERT=demand /tmp/fixture/openldap/bin/ldapwhoami -x -H ldaps://127.0.0.1:1636 -D "cn=fixtureadmin,dc=fixture,dc=test" -y /tmp/fixture/admin-password > /tmp/fixture/artifacts/readiness.log 2>&1; then ready=yes; break; fi
 sleep 0.1
done
test "$ready" = yes
IAM103_FIXTURE_CONFIG=/tmp/fixture/config.json /tmp/directory.test -test.v -test.timeout=45s -test.run='^TestLive' > /tmp/fixture/artifacts/live-tests.log 2>&1
printf 'IAM103_LIVE_PASSED\n'
