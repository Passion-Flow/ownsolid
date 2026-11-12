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
