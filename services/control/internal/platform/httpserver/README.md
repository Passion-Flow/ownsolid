# HTTP transport and response boundary

This package owns the control service listener and reusable wire behavior. It does not resolve tenant, Workspace, actor or object authorization. Domain HTTP handlers supply already authorized results; [server composition](../../../cmd/server/runtime_lifecycle.go) owns routing, TLS material and the lifetime of all workers.

## Entry points and policy

[ServeConfigured](server.go) takes the validated named `config.ServerConfiguration`,
its actual TLS configuration, handler and HTTP policy. It selects direct TLS or
an explicitly configured plain HTTP/proxy backend while retaining connection,
header/request/idle deadlines and bounded shutdown. The historical `Serve`
entrypoint requires nonnil TLS; it cannot silently downgrade.

[ListenConfigured](server.go) prebinds the browser sockets and optional dedicated
`config.MachineServerConfiguration` HTTPS socket before accepting requests. Its
owned `ListenerGroup.Serve(ctx)` starts once; `Close()` before Serve releases
all sockets, while Close during Serve cancels requests and joins every bounded
shutdown call. A bind failure closes the earlier sockets. All listeners share
one `HTTPPolicy.MaxConnections` channel, rather than doubling the configured
budget. Handler active-request admission remains with the shared identity owner.
Caller composition must validate the actual browser and machine TLS material,
retain both handlers and start business workers only after prebinding succeeds.
TLS configurations are cloned for the owned listeners and must not be mutated
after submission. This package does not select certificate files or create SQL.

Optional `server.portal_listen_address` selects a Portal-only socket and makes
`server.listen_address` Admin-only. `browserEntrance` resolves the original
request with the existing transport owner and refuses the sibling audience
before assets, health or business handling. It never rewrites Host or TLS. The
same identity handler and active-request admission remain shared. Omission keeps
the historical shared browser socket. `ServeConfigured` also honors this split
through an owned listener group; it cannot silently ignore the extra bind.
Refusals retain the correlated transport audit and fixed per-audience
`ownsolid_http_entrance_rejections_total` counters from `ListenerGroup.Samples`;
the production serving composition adds them to its existing metrics pusher.
See [configuration and remaining qualification](../../../../../docs/research/entrance-listeners.md).

[machine_request.go](machine_request.go) resolves only real direct TLS, the
configured machine authority/SNI and the actual socket peer. It rejects all
forwarding headers and any Origin/Cookie header, including empty or differently
cased field names. Its immutable request context retains HTTPS/admin and the
actual machine origin for the existing machine dispatcher. The identity owner
must separately enforce the closed agent/gateway route families and all existing
credential/lease/License/Workspace checks; this resolver grants none of them.

[request_transport.go](request_transport.go) validates approved authority, actual
SNI/TLS or the trusted socket peer/exact single-hop proxy triple. Typed context
retains effective client identity separately from real socket provenance; it
never manufactures `r.TLS`. Its correlated operational audit logs only fixed
validated connection fields and request ID. Business audit/tenant authorization
remain domain-owned. Direct modes reject forwarding headers. Machine endpoints
retain HTTPS requirements independently from browser HTTP.

[cookies.go](cookies.go) is the shared immutable issue/read/clear policy for
identity and SSO. Secure modes use `__Host-`, explicit HTTP uses
`projectone_http_`; both preserve host-only audience-scoped names, HttpOnly,
Path=/ and existing SameSite semantics, without alternate-name fallback.

The parent injects `server.listen_address`, certificate references and the validated `runtime.http` and server transport policy. Use the [HTTP policy env example](../../../../../config/examples/control/http-policy.env.example) and [configuration owner](../config/README.md); do not read ambient environment or derive public origins from the listener. `MaxJSONBytes` in [json.go](json.go) is the versioned API envelope bound, not a tunable deployment URL or worker budget.

## Requests, problems and assets

`DecodeJSON` checks media type, UTF-8, duplicate/null values, trailing input and unknown fields. `DecodeRequiredObject` in [strict_request.go](strict_request.go) additionally requires the exact DTO field set, including explicit empty objects where the DTO allows them. Choose the decoder required by the endpoint contract; they are not identical validation APIs.

[problem.go](problem.go) maps recognized public codes to HTTP problems and preserves reconciliation behavior for unknown commits. Generated Registry/task-delivery status tables belong to their corresponding contract generators. Never put raw database, provider or credential diagnostics into public error codes.

[Assets](assets.go) serves the configured frontend build root and SPA entry. It is not the localization inventory loader or a user-upload endpoint. [Download](download.go) carries an already verified file descriptor, bounded range, digest and attachment metadata. `ServeDownload` consumes that descriptor on every outcome, closes it on request cancellation, and aborts a response after a streaming failure rather than appending JSON to partial artifact bytes. Authorization and artifact integrity must be established by the borrowing domain before this call.

## Lifecycle and verification

Dedicated Agent connections expose their owned drain and hard-close times through
`ConnectionDrainAt` and `ConnectionCloseAt`. The gateway can advertise relative
durations after authentication. These values come from the accepted TLS socket,
and request headers cannot choose or extend them. Shared listeners return zero
times; their ownership and shutdown behavior are unchanged.

`TestTransportActualIPv6HTTPAndHTTPS` uses real IPv6 loopback sockets for localhost
and bracketed `::1` origins, normal DNS/IP SAN verification, distinct audiences and
mode-correct cookies. The focused package race run passes locally. It does not
establish external IPv6 routing, customer PKI or Docker host networking qualification.

Cancellation initiates a bounded `http.Server.Shutdown`; failure closes the server and is returned. The independent server runtime joins domain workers and selected database cleanup. A successful listener shutdown alone says nothing about those domain side effects.

The listener group cancels sibling request contexts after any listener failure,
waits for every Serve and concurrent Shutdown call, and returns fixed redacted
`HTTP_LISTENER_*` failures. A drain timeout forces socket closure and returns
`HTTP_LISTENER_SHUTDOWN_FAILED`; it does not claim that a handler ignoring its
context completed. Callers must preserve that failure and independently resolve
any outstanding business cleanup. Nil Serve contexts and repeated starts are
refused. Call Close after a rejected invocation to release prebound ownership.

New focused source roots are `TestMachineRequestTLSAuthorityAndSocketContext`,
`TestMachineRequestRejectsTransportBrowserAndAuthorityConfusion`,
`TestMachineListenerGroupActualHTTPAndVerifiedTLS`,
`TestMachineListenerGroupPrebindFailureAndCloseBeforeServe`,
`TestMachineListenerGroupSharesConnectionBudget`,
`TestMachineListenerGroupJoinsBothRequestCleanupsAndRejectsRepeatStart`,
`TestMachineListenerGroupSiblingFailureCancelsBoth`,
`TestMachineListenerGroupShutdownDeadlineClosesBoth`, and
`TestMachineListenerGroupRejectsInvalidSelections`. Controlled request-state
tests and actual loopback HTTP/TLS tests are distinct: the latter use synthetic
local handlers/certificates with normal CA/name verification and no verifier
bypass. They do not prove real domain SQL or installed deployment behavior.
These roots are source-only until the owning measured runner executes them;
historical transport evidence below is not acceptance of this new increment.

With the pinned Go toolchain and admitted cache/temp roots, the focused package check is:

```sh
go -C services/control test ./internal/platform/httpserver
```

The tests exercise actual local TLS shutdown ordering, request decoding, descriptor ownership, partial-response behavior and public error redaction. They do not establish browser certificate trust, external-origin compatibility or target deployment acceptance. When changing a request shape or public failure code, update the owning endpoint contract, frontend client and generated status checks together.

The first coordinated source gate passes native race17roots/197entries, real
loopback TLS/proxy/plain-listener controls, vet and full native/Linux compilation.
This is not complete SQL/login/browser/address/deployment acceptance; see
[exact evidence and remaining work](../../../../../docs/execution/mvp/deployment-transport-boundaries.md).

## Configured retry advice

`ProblemWithPolicy` accepts the serving owner's validated `config.HTTPPolicy`;
`Problem` preserves the same owned defaults for standalone compatibility. Real
positive typed rate metadata wins over configured fallback; all503responses use
configured service advice. Other statuses clear stale advice, including unknown
commit409. Invalid directly injected policy fails as redacted SERVICE_NOT_READY.
No response-writer wrapper or automatic replay is introduced. The identity
dispatcher and localization route explicitly inject the same HTTP policy. See
[contract and focused tests](../../../../../docs/engineering/deployment-transport/current-m01-retry-policy.md).
