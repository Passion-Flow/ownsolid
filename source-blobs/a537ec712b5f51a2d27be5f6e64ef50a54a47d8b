# Installer command cancellation (REL-106)

## Sources and current boundary

The pinned [Go os/exec implementation](https://cs.opensource.google/go/go/+/refs/tags/go1.27.3:src/os/exec/exec.go)
and [CommandContext contract](https://pkg.go.dev/os/exec#CommandContext)
set the default cancellation action to kill the direct child. WaitDelay is
unset, so a descendant retaining an output pipe can keep Wait pending after
that child exits. The installer uses that unchanged default in ExecRunner.Run
and ExecRunner.Stream. These methods run local Docker and Compose clients;
a client process and a daemon-side exec process are separate lifetimes.
[Docker exec](https://docs.docker.com/reference/cli/docker/container/exec/)
starts an additional command in an existing container. Its documentation does
not establish that an interrupted client has joined all daemon-side work.

The existing Control Dameng process runner owns and checks a process group,
bounds retained output and uses a typed termination grace. It is a design
reference, not an installer dependency: Go internal-package boundaries prevent
the offline installer from importing that owner.

## Prototype before selecting a fix

First use a temporary Go test overlay outside product code to invoke the actual
ExecRunner.Stream against a short-lived helper parent and child. Wait for the
helper to start, cancel the context, and measure when the caller returns.
Every helper must end naturally within its independent test timeout. This
observes the local inherited-pipe behavior without Docker, SQL or services.
A Mac result is a development prototype, not Linux installation qualification.

Then compare direct Docker exec and Compose on one expressly owned, bounded,
network-none Linux container from an already staged image. Retain the exact
image/client versions and inspect whether daemon-side work survives cancellation.
This second prototype needs its own resource opportunity; no run is claimed here.
No PostgreSQL, live installation, host port or foreign container is needed.

Reject assuming that a local client exit proves remote completion, accepting
a return-time bound as proof of process cleanup, or signalling arbitrary host
PIDs without current ownership. A WaitDelay alone can close local pipes without
stopping remote work. Select a production change only after the actual failure
and cleanup authority are proven. Preserve logical pg_dump/pg_restore, pending
recovery records, original-database authority and explicit retry behavior.

Regression acceptance must cover ordinary exit and output, cancellation during
output, inherited pipes, owned-child cleanup, and the actual Docker/Compose
protocol. Test backup and restore refusal/retry separately before claiming the
installer path complete. REL-106 also retains its other component owners.

No dependency, model or dataset is added. The existing Go standard library uses
BSD-3-Clause, and the host Docker CLI and Compose use Apache-2.0. These allow
commercial/offline use and redistribution with their required notices. No
upstream process implementation is copied. Any later image prototype must pin
its existing image and review that image's notices before execution.

## Local inherited-pipe observation

On macOS arm64 with Go 1.27.3, a temporary test overlay called the actual
[ExecRunner.Stream](../../deploy/offline/internal/install/backup.go) from source
620f76da. The helper parent started a child that retained its output handles
for two seconds. After the helper was ready, cancellation killed the direct
parent, but Stream returned 2.011098 seconds later. The original source stayed
unchanged. The native Linux test executable was cross-built successfully.
This is a local development observation; Linux and real Docker/Compose process
lifetime remain to be measured.

The next transport observer must invoke the same actual installer method with
explicit Docker/Compose arguments. Independently inspect only its owned
container after the local client returns. Record return delay and any remaining
helper processes separately. A successful observer run means the experiment
completed; it does not mean a production cancellation fix passed. Preserve the
original local observation, and keep all product execution code unchanged
until the transport and cleanup authority have been established.

## Cached-image inventory boundary

A never-started Linux amd64 container inspection bound the existing experimental
image `sha256:173f284a4299164772a90f52b373e73e087583c0963f1334c9995f190ef6f3f5`
to Alpine 3.23.2 and 76 declared APK package records. The installed database
SHA-256 is `6963734ac4ecc5bd91278e71fc763a4fef0c3334abe46eb9600a8696e1f270f9`.
The `image-inventory-native.ramhsq0_` record confirms created/PID 0/not running,
no container code execution, and removal of its own container and anonymous
volume. Copying `/usr/share/licenses` returned exit 1; that is retained as an
unresolved notice-copy result, not evidence that notices are absent.

The planned experiment overrides the image entrypoint with a bounded shell
sleep and executes only the separately mounted static Go helper. It does not
start the embedded Docker engine, CLI, Compose or Buildx. Host Docker 29.1.3 and
Compose 2.40.3 remain separate clients with Apache-2.0 licences; do not confuse
them with embedded Compose 5.0.0 or Buildx 0.30.1.

BusyBox and busybox-binsh 1.37.0-r30 declare GPL-2.0-only. The
[BusyBox licence guidance](https://busybox.net/license.html) and
[GPL v2 section 0](https://raw.githubusercontent.com/torvalds/linux/v6.8/LICENSES/preferred/GPL-2.0)
distinguish running a program from redistribution. The proposed internal,
offline process-lifetime experiment does not redistribute this image. A shipped
BusyBox binary needs the applicable licence, notices and complete corresponding
source under the selected GPL option; do not treat an upstream URL as an offline
source package.

Musl 1.2.5-r21 declares MIT. The official
[musl 1.2.5 source archive](https://musl.libc.org/releases/musl-1.2.5.tar.gz)
COPYRIGHT was read: its MIT grant permits use, copying, modification and sale,
with copyright and permission notices retained. This includes commercial and
offline use. The source notice SHA-256 is
`f9bc4423732350eb0b3f7ed7e91d530298476f8fec0c6c427a1c04ade22655af`.
This checks upstream terms, not the correspondence of Alpine revision r21 to a
particular built binary. The Go helper is built with CGO disabled and does not
link musl. The existing Go standard-library BSD-3-Clause and host client
Apache-2.0 terms permit commercial/offline use and redistribution with required
notices. No dependency, model or dataset is added to the product.

The table below preserves every package/version and its declared licence from
that immutable image. These are package declarations, not a reviewed release
SBOM or approval to redistribute the whole image. Source correspondence,
patches, notices and any licence expression choices remain release-inventory
work in DEL-110. The image is an experimental fixture, not a product payload.

| Declared licence expression | Package versions |
| --- | --- |
| Apache-2.0 | libcrypto3 3.5.4-r0, libssl3 3.5.4-r0, openssl 3.5.4-r0 |
| BSD-3-Clause | inih 62-r0, libbsd 0.12.2-r0, libedit 20251016.3.1-r0, libtirpc 1.3.5-r1, libtirpc-conf 1.3.5-r1, libuuid 2.41.2-r0, linux-pam 1.7.1-r1, pcre2 10.47-r0, shadow-libs 4.18.0-r0, shadow-subids 4.18.0-r0 |
| BSD-3-Clause AND BSD-2-Clause AND ISC AND Beerware AND Public Domain | libmd 1.1.0-r0 |
| BSD-3-Clause OR GPL-2.0-or-later | zstd-libs 1.5.7-r2 |
| CDDL-1.0 | zfs 2.4.0_rc5-r0, zfs-libs 2.4.0_rc5-r0 |
| GPL-2.0-only | alpine-baselayout 3.7.1-r8, alpine-baselayout-data 3.7.1-r8, apk-tools 3.0.3-r1, busybox 1.37.0-r30, busybox-binsh 1.37.0-r30, git 2.52.0-r0, git-init-template 2.52.0-r0, libapk 3.0.3-r1, scanelf 1.3.8-r2, ssl_client 1.37.0-r30 |
| GPL-2.0-or-later | btrfs-progs 6.17.1-r0, eudev-libs 3.2.14-r6, iptables 1.8.11-r1, iptables-legacy 1.8.11-r1, libip4tc 1.8.11-r1, libip6tc 1.8.11-r1, libnftnl 1.3.0-r0, libxtables 1.8.11-r1, lzo 2.10-r5 |
| GPL-2.0-or-later AND 0BSD AND Public-Domain AND LGPL-2.1-or-later | xz 5.8.1-r0, xz-libs 5.8.1-r0 |
| GPL-2.0-or-later AND LGPL-2.0-or-later | keyutils-libs 1.6.3-r4 |
| GPL-2.0-or-later AND LGPL-2.0-or-later AND BSD-3-Clause AND MIT | e2fsprogs 1.47.3-r0, e2fsprogs-extra 1.47.3-r0, e2fsprogs-libs 1.47.3-r0, libcom_err 1.47.3-r0 |
| GPL-2.0-or-later OR LGPL-3.0-or-later | libidn2 2.3.8-r0, libunistring 1.4.1-r0 |
| ISC | skalibs-libs 2.14.4.0-r0, utmps-libs 0.1.3.1-r0 |
| LGPL-2.1-or-later | libblkid 2.41.2-r0, libintl 0.24.1-r1, libmnl 1.0.5-r2, userspace-rcu 0.15.3-r0, xfsprogs 6.17.0-r0 |
| MIT | alpine-keys 2.6-r0, alpine-release 3.23.2-r0, brotli-libs 1.2.0-r0, c-ares 1.34.6-r0, krb5-conf 1.0-r2, krb5-libs 1.22.1-r0, libeconf 0.8.0-r0, libexpat 2.7.3-r0, libpsl 0.21.5-r3, libverto 0.3.2-r2, musl 1.2.5-r21, nghttp2-libs 1.68.0-r0, nghttp3 1.13.1-r0 |
| MIT AND BSD-2-Clause AND GPL-2.0-or-later | musl-utils 1.2.5-r21 |
| MPL-2.0 AND MIT | ca-certificates 20251003-r0, ca-certificates-bundle 20251003-r0 |
| SSH-OpenSSH | openssh-client-common 10.2_p1-r0, openssh-client-default 10.2_p1-r0, openssh-keygen 10.2_p1-r0 |
| X11 | libncursesw 6.5_p20251123-r0, ncurses-terminfo-base 6.5_p20251123-r0 |
| Zlib | pigz 2.8-r1, zlib 1.3.1-r2 |
| curl | libcurl 8.17.0-r1 |

## Transport experiment admission

The original running-container prototype has not run. Preserve its original
parent, configuration and cross-built helper. A reviewed execution variant must
use public-field inspect projections, an owned empty Docker configuration, and
exact image plus run-label ownership before cleanup. Add explicit anonymous
volume removal readback. Keep the original HOME unchanged, bound the parent
through child join and evidence, and retain actual refusal or timeout results.
A metadata inspection or a licence review is not a resource opportunity to run
container code.

Compare direct Docker exec and Compose against the actual installer Stream,
then independently observe owned remote helpers after the local caller returns.
Report both durations and remaining work. A completed observation does not
establish a cancellation fix or a zero-egress capture. Select product changes
only after this native transport boundary is observed, then qualify logical
backup/restore refusal and retry separately.
