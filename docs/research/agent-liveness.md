# Agent liveness at fleet scale

Research behind ARC-010 and [ADR 0022](../decisions/0022-liveness-outside-the-database.md).
ARC-010 was built before research notes were required. This note records the sources afterwards
and what they do and do not support.

## How others solve it

**Kubernetes** split the same problem in two. A kubelet renews a small Lease object, by default
every 10 seconds, and that renewal is independent of updates to the Node's `.status`, which it
writes when the status changes or, by default, every 5 minutes
([node status](https://kubernetes.io/docs/reference/node/node-status/#heartbeats)). The control
plane judges availability from the Lease's `spec.renewTime`
([leases](https://kubernetes.io/docs/concepts/architecture/leases/)). The motivation was write
cost: the proposal estimated etcd writes falling from 150 MB/min to 30 MB/min at 5,000 nodes
([KEP-589](https://www.kubernetes.dev/resources/keps/589/)). That figure is the proposal's own
estimate, not a measurement we repeated.

ownsolid takes the same split and goes one step further: a repeat is not written per node at all
but batched, because PostgreSQL rather than etcd holds the state and commits are what it runs out
of.

## Replicated store

| Option | Licence | Offline delivery | Why chosen or rejected |
| --- | --- | --- | --- |
| NATS JetStream KV | [Apache 2.0, server](https://raw.githubusercontent.com/nats-io/nats-server/main/LICENSE) and [client](https://raw.githubusercontent.com/nats-io/nats.go/main/LICENSE) | One static binary and image | **Chosen.** Already a dependency of the task bus |
| Valkey with replication | Not assessed in depth | Possible | A second broker alongside NATS, with no gain for this data |
| Embedded consensus in control | Own code | No new image | A protocol to own and test for data that needs none |
| PostgreSQL | Already present | Already present | The load this item removes |

What the NATS documentation supports:

- A bucket is a stream `KV_<bucket>` whose subjects are `$KV.<bucket>.>`, and a value is a message
  on the key's subject ([key-value store](https://docs.nats.io/nats-concepts/jetstream/key-value-store)).
  The replica publishes to that subject directly, so a batch shares one pipeline.
- A replicated group works while more than half its members are reachable. Three copies are the
  stated production floor and five the maximum
  ([surviving node loss](https://docs.nats.io/learn/jetstream/surviving-node-loss)), so
  configuration accepts 1, 3 or 5.
- Memory storage "suits data you can afford to lose on a restart" (same page). Sightings are that
  data: the database and the next heartbeats restore them.
- A bucket TTL expires every value at that age, while per-key TTL needs limit markers
  ([TTL and limits](https://docs.nats.io/learn/key-value/ttl-and-limits)). Every sighting rewrites
  its key, so the bucket TTL is enough and no limit markers are needed.

Sightings are per-node maximum timestamps, so a reader that keeps the largest value needs no
ordering between writers. This is reasoning from the data, not a claim from a source.

## Failure modes and their tests

| Failure | Behaviour | Test |
| --- | --- | --- |
| Control restarts | First heartbeat of each node takes the full path; no node turns worse until the thresholds pass since the start | `TestRestartDoesNotMarkTheFleetOffline`, end-to-end restart |
| Report waits while state changes | Table drops the older report and each statement compares times | `TestAcceptDropsAnOlderPendingReport`, `TestUnchangedObservationsReachTheRowInBatches` |
| Row held by a long transaction | Skipped, written with the next heartbeat | `TestFlushSkipsARowAnotherTransactionHolds` |
| Database unavailable during a flush | Batch returns to the table | `TestFailedFlushKeepsSightingsForTheNextPass` |
| Node disabled, removed or out of scope | The read sees it and the full path decides | `TestFastPathRefusesWhatTheSynchronousPathRefuses` |
| Store stalled or down | Publishing stops for the pass after a bounded wait | `TestAStalledReplicaDelaysAFlushByItsBoundOnly` |
| One of three servers lost | Data and service survive | `TestJetStreamReplicaAcrossThreeServers` |
| Unknown key or certificate authority | Refused | `TestJetStreamReplicaRefusesWhatItCannotTrust` |

## Open questions

- Row writes per second stay equal to the heartbeat rate while the flush interval is shorter than
  the heartbeat interval. ARC-015 measures whether that is enough at load.
- The store's write rate at 10,000 nodes has not been measured. The test published 1,000 sightings
  to three servers in about 25 ms on one Docker host, which says little about a real network.
