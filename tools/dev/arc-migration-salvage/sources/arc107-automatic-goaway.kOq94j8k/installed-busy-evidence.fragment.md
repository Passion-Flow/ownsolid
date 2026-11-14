

## Installed lifecycle run with busy-task boundaries

The isolated native Linux amd64 run on 149a9501 exercised 500 individually enrolled
simulated agents through the actual installer, schema 60 PostgreSQL and two gateway
processes. It retained the two-CPU/6 GiB outer fixture, network none, original
three-minute move/two-minute dispatch limits and 100-second work. Its source delta
from 59c9c86a contains only research and two e2e files; the unchanged production
binaries were reused byte for byte after complete source verification. The updated
native e2e race/vet passed before the installed run. The actual selected test
**failed** at its terminal report assertion and is not a completed acceptance run.

| Observed stage | Result |
| --- | --- |
| Enrollment / every agent connected | 500 agents in 2 s / 11 s |
| Original preferred sessions | 258/242; 25 acknowledged tasks |
| All original transports retired | 65.4 s; no original transport forced closed |
| A observed drain / retirement ages | 54.011145–59.981629 s / 54.233425–68.994617s |
| B observed drain / retirement ages | 54.063368–59.980572 s / 55.569327–69.194800s |
| Maximum observed drain scheduling delay A/B | 0.040451 s / 0.026864s |
| First batch / all original transports replaced | 25 successful tasks / 99.4 s |
| Idle agents on surviving gateway after kill | 475 in 26.8 s; 25 busy tasks retained valid leases |
| Second batch / all agents on survivor | 25 successful tasks / 500 agents in 101.3 s |
| Original preferred distribution restored | 258/242 in 44.5 s after return |
| Lease renewals / results | 600 successful renewals, no refused renewal; 50 results |
| Terminal generator report | 497 connected and on stream out of 500; zero revoked |
| Egress |No external attempt; one recorder probe observed |

The last generator window recorded 500/ 500, but the stop-time snapshot recorded 497
while repeated 60-second age cycles continued. The strict 500 terminal assertion
remains unchanged. Its failure occurred before the final persisted-cursor readback;
that final gate was not executed. The earlier lifecycle, in-flight lease and exact
attempt assertions do not substitute for that missing acceptance. Preserve the
complete report, 500-agent protocol logs, raw observer rows and original failure.
Do not lower the threshold, report a successful selected test or tick ARC-107.

There were 122 observer rows at a declared five-second sampling interval, including
23 deliberately stopped A rows. A's sampled maximum was 258 established serving-port
sockets, 90.18 MiB Docker CLI cache-adjusted memory, 30.51 percent CPU and 21 PIDs.
B's was 500 sockets, 165.3 MiB memory, 34.56 percent CPU and 22 PIDs. TIME_WAIT maxima
were 32/43 and are kept separate from live sockets. These are sample maxima from
this functional fixture, not instantaneous peaks, RSS or a 10,000-node capacity
result. They do not prove sustained scale or real hardware behavior.

The selected run log SHA-256 is
053604392a15ab01720727d0b8dbc4bc7aec69ad858f4e415a8bd3f08c3a397d.
The owned outer container, nested database/workloads and staging directory were
cleaned. Shared test-server containers, the previous release dumps and all foreign
resources were left untouched. Gateways still share control's database role
(ARC-111); shared-listener integration also remains outside this dedicated path.
