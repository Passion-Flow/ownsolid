# Lane SCI integration boundaries

## Audit P1

| Item | Required integration |
| --- | --- |
| AUD-101 | IAM-109 service accounts and AGT-107 product-agent mutations must provide authenticated actor identities. The audit search projection must expose those actor types. |
| AUD-103 | OBS-103 and the application, gateway, model-selection and execution owners must propagate one trace. A correlation ID on a database event alone cannot demonstrate GPU execution. |
| AUD-104 | OBS-102 and the delivery configuration must apply the six-month minimum to network log stores. The audit table's retention is a separate policy. |
| AUD-106 | IAM-107 must supply the auditor role, with an explicit permission for read-only search enforced by the audit route and exposed by the console. |
| AUD-107 | The authenticated export and approval flow needs the authorization and console boundaries. The database operator's integrity export covers the separate AUD-102 use case. |
| AUD-108 | Diagnostic entry points must record access before serving the redacted data, including the support collector used while control is down. |
| AUD-109 | The database, object-store, backup, deployment and TLS owners must agree on key custody, rotation, recovery and the first KMS fixture. |
| AUD-111 | The [operator forwarder](audit-siem-delivery.md) has tenant-scoped TLS and a durable cursor. Continuous delivery still needs trusted startup registration, installation and tenant grants, a least-privilege export source or indexed database reader, a persistent operator volume, and an approved site SIEM. |

The open items span entry points outside the lane. Leave the audit writer
transactional and retain the current public contracts while those entry points
are unavailable. AUD-102 is an existing completed item. Its operator procedure
makes the pre-migration gap and independent checkpoint custody explicit.

## Evaluation P1

EVL-101 needs immutable dataset and model versions from DAT-106 and MOD-103 and
an execution profile from serving. EVL-102 compares those identities. EVL-103
has [native CPU refusal and pass evidence](evaluation-reproducibility.md#evl-103-retained-preparation-quality-decision).
Production acceptance still needs a NVIDIA attempt, authenticated physical
stop and authorized audited promotion. EVL-104 needs a scoped OBS-103 trace
export with content redaction and explicit regression case approval. EVL-105
needs a real offline serving workload with a pinned deployment, accelerator
identity, request timings and metered usage or cost inputs. The CPU preparation
capture establishes quality on its own holdout. EVL-106 has deterministic
replay and still needs real engine and authorized interface evidence. No evaluation-only registry or substitute promotion path will
provide those guarantees.

A scorer probe on Linux amd64 ran inside an isolated network namespace. Three
Python processes replayed fixed classification predictions with different
hash seeds and produced the same two correct results out of four and the
same confusion matrix. The probe refused missing predictions and distinguished
the exact 50.00% threshold from 50.01%. It used the inherited scorer and the
checked-in recipe with a temporary path mapping for its legacy contracts
directory. The probe covered scoring only. No fresh inference or GPU
repeatability profile was established.

## Scientific computing P1

SCI-101 must use SCH-101, MOD-110 and the versioned artifact boundary from
NET-101. Starting a separate queue or accepting a client assertion of rights
would bypass the required admission check. SCI-102 through SCI-108 build on that
admitted run and require the task entry points and storage owners. SCI-109
requires the admitted batch path and a 72-hour run with four concurrent GPU
tasks. The Linux development host has no GPU. The licensing policy and test
cases are in the [module research](scientific-computing.md). No restricted
model or third-party data is bundled or needed to test admission decisions.

A dependency-free policy probe in a disconnected Linux namespace admitted one
synthetic matching grant and refused six cases covering missing authorization,
non-commercial scope, another tenant, another artifact, expiry and revocation.
It established the local decision rules only. No production authority or
scheduler called this probe.
