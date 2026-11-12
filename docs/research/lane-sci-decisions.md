# Lane SCI integration boundaries

## Audit P1

| Item | Required integration |
| --- | --- |
| AUD-101 | IAM-109 service accounts and AGT-107 product-agent mutations must provide authenticated actor identities. The audit search projection must expose those actor types. |
| AUD-103 | OBS-103 and the application, gateway, model-selection and execution owners must propagate one trace. A correlation ID on a database event alone cannot demonstrate GPU execution. |
| AUD-104 | OBS-102 and the delivery configuration must apply the six-month minimum to network log stores. The audit table's retention is a separate policy. |
| AUD-106 | IAM-107 must supply the auditor role, with an explicit permission for read-only search enforced by the audit route and exposed by the console. |
| AUD-107 | The authenticated export and approval flow needs the authorization and console boundaries. The database operator's integrity export covers the separate AUD-102 use case. |
| AUD-108 | License diagnostics and the installation-wide identity read already append access events. The [scoped append contract](audit-diagnostic-access.md#scoped-append-contract) binds the exact workspace and local site/cell for IAM to pair with its workspace reader. Delivery retains both support entry points and Control-down admission/reconciliation. The audit module owns no diagnostic read handler. |
| AUD-109 | The database, object-store, backup, deployment and TLS owners must agree on key custody, rotation, recovery and the first KMS fixture. |
| AUD-111 | The [operator forwarder](audit-siem-delivery.md) has tenant-scoped TLS and a durable cursor. Continuous delivery still needs trusted startup registration, installation and tenant grants, a least-privilege export source or indexed database reader, a persistent operator volume, and an approved site SIEM. |

The open items span entry points outside the lane. Leave the audit writer
transactional and retain the current public contracts while those entry points
are unavailable. AUD-102 is an existing completed item. Its operator procedure
makes the pre-migration gap and independent checkpoint custody explicit.

## Evaluation P1

EVL-101/102 already have scoped dataset, model and preparation readers.
The [authorized report service handoff](evaluation-reproducibility.md#authorized-report-service-handoff)
identifies the missing report operation, immutable persistence, current-authority
publication and authenticated route. A preparation report currently follows its
latest attempt; the evaluation must retain the selected attempt and raw capture.
An identical content digest grants no cross-workspace access. The control
evaluation module, identity composition, versioned contract and UI remain outside
the currently allocated SCI paths. No alternate report registry is selected.

EVL-103 has [native CPU refusal and pass evidence](evaluation-reproducibility.md#evl-103-retained-preparation-quality-decision).
Production acceptance still needs a NVIDIA attempt, authenticated physical
stop and authorized audited promotion. EVL-104 needs a scoped OBS-103 trace
export with content redaction and explicit regression case approval. EVL-105
has [two authorized real CPU pairs](evaluation-performance-benchmark.md#authorized-cpu-gateway-pair)
with scoped acknowledged receipts. UI, detached signing, GPU performance and
cost acceptance remain open. EVL-106 retains three real CPU adversarial
responses as unverified and a separately identified refusal control. Its
authorized capture/report interface and semantic safety acceptance remain open.
The fixed EVL-105 greedy samples provide no additional safety qualification.

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
