# Portal

What the people who *use* the platform see, as opposed to the people who run it.
Scope is always the person's own workspace, inside the quota an administrator gave
them, and it is enforced from the authenticated session on the server.

Added 2026-11-06 after [ADR 0035](../../decisions/0035-what-belongs-in-each-entrance.md).
The console had 34 pages and the portal two, because pages were written where the
work was happening rather than where their audience could reach them. These items
move the self-service half of each capability to the entrance its audience uses. The
administrative half stays in its own module and is not duplicated here.

Reviewed 2026-11-08 against OpenAI's [API key guidance](https://help.openai.com/en/articles/5112595), LiteLLM's [internal user self-serve](https://docs.litellm.ai/docs/proxy/self_serve) and Open WebUI's [API keys](https://docs.openwebui.com/features/authentication-access/api-keys/). The item list holds: each of them scopes a person to their own keys, shows the secret once and favours expiry and rotation, which POR-106 covers. One gap is not the portal's to close: LiteLLM and Open WebUI let an administrator decide who may issue personal keys at all, and ownsolid lets every workspace member. That is a server policy, so it is GW-119 in the gateway module.

- Owns: `web/apps/portal`.
- Depends on: identity-and-access for the session and the workspace scope; each
  capability's own module for the API it calls. No item here adds a new backend
  capability; each one is the portal's view of one that exists.

## P1 Enterprise release

- [ ] POR-101 Portal shell: the person's workspace switcher, grouped navigation, command menu, and a home that answers what is running, what it cost and what needs them (P1)
- [ ] POR-102 Assistants and conversations: chat with what has been published to the workspace, with history that never leaves the installation (P1)
- [ ] POR-103 Applications: open and install the apps an administrator published to this workspace, and nothing else (P1)
- [ ] POR-104 Model catalogue for users: which models this workspace may call, what each one costs, and what it is good for (P1)
  - The page shows which models may be called and every fact the server holds ([research](../../research/portal-model-catalogue.md)). It stays open until prices (TEN-107) and installed Console-to-Portal description acceptance (GW-120) are complete.
  - S-304 reads the authorized alias description. Schema 59 and the description API are published. Console edit/clear, conflict review, retry and Undo, and the read-only Portal projection passed local bilingual Chromium checks at 60b85e03 ([research](../../research/gateway-model-description.md#web-consumer-qualification)). The earlier predecessor/candidate HTTPS check does not replace same-source installed acceptance; repeat the complete flow on the next production installation.
- [ ] POR-105 Playground: try a model against a prompt, compare two, and copy the call that produced the answer (P1)
- [x] POR-106 API keys and endpoints: issue, name, rotate and revoke my own keys; the endpoint, a code sample per language, and my own call log (P1) - Evidence: c20a9df, 4b62fef; live run on an installation, 2026-11-08: a non-administrator invited through a real mail flow issued, rotated and revoked a key from the portal against a real control plane and database, calls admitted over the real gateway protocol by a stand-in for the gateway process (GW-114) and read per key; an administrator's portal session got 404 for that key, and another workspace was refused. No model ran; the three samples were run against a local HTTPS stub with the real OpenAI SDKs and curl
  - Native gateway acceptance in `23b99915` matched five serial HTTPS calls to the member's Portal history, refused the revoked key before forwarding, and retained the cross-workspace refusal. It used the real Rust gateway and Control with PostgreSQL and a loopback tokenizer and completion fixture ([verification](../../research/browser-suite-replacement.md#native-gateway-for-the-portal-key-flow)).
- [ ] POR-107 Jobs: submit training, batch and scientific jobs, watch their steps and logs, open their results, and re-run one (P1)
- [ ] POR-108 Workspaces: notebooks and development environments I own, with the resource they hold shown against my quota (P1)
- [ ] POR-109 Data: my datasets and files, their size against my storage quota, and what a job did with them (P1)
- [ ] POR-110 Evaluation: start a run against a model I may call, and read the score beside the runs before it (P1)
- [ ] POR-111 Usage and quota: tokens, GPU hours and budget used against what I was given, with the day it will run out (P1)
- [ ] POR-112 Account: my profile, my password, my sessions, and the ones I want ended (P1)
  - S-308 specifies the existing shared Account page. Corrected source `0a50a040` passed affected lint/types, 33 Shell and 34 UI tests, both exact-source builds and 11/11 scripted browser cases per entrance; all 56 new raw screenshots were read after cleanup. This independently qualifies the `6d3d6b8c` capture/heading correction; earlier failures and 70/36 image evidence remain separate. Full gates, later DEV-source composition and installed offline authority/audit/real-SSO acceptance remain open ([research](../../research/portal-account-sessions.md)). No candidate is published as fully qualified.
  - Frozen original-author joint source `82c602d3` passed original install/check/build. Its full test attempt exited 2: Console 1141 passed, 81 local-preview navigation failures, 28 existing conditional skips; Portal browser and native/unit tests were not entered. All 11 Console Account-session cases passed inside that failed suite. Cleanup and exact inputs were verified; no publication or retry followed. The complete gate, DEV compatibility review and installed acceptance remain open ([boundary and evidence](../../research/portal-account-sessions.md#original-joint-full-gate-and-loopback-failure)).
  - A separately admitted directed run on unchanged `82c602d3` passed all 129 cases in the four selected files, with eight accidentally matched original live cases skipped and zero retries. Exact comparison corrected the earlier coverage claim: 71 old failures passed, ten `node-page.spec.ts` failures were outside that plan. The observed preview had one verified birth/listener and 92 HTTP-200 probes; the old failure did not reproduce and its cause remains unknown. No complete-gate or installed acceptance is inferred ([results and limits](../../research/portal-account-sessions.md#directed-browser-observation-and-corrected-coverage)).
- [x] POR-113 In-product help that works offline, reachable from every page (P1) - Evidence: 779c1558 and the [portal Chromium help flows](../../../web/apps/portal/e2e/help.spec.ts); the existing offline installation run served the articles and portal-only API reference, and local Chromium on 2026-11-09 opened the contextual article on every signed-in portal screen with an article, checked the fallback and filtered empty states, and reached help without requests outside the installation origin
- [ ] POR-114 Every portal page reads its scope from the session, with a test that a parameter naming another workspace is refused rather than filtered (P1)

## P2 Fleet operations

- [ ] POR-201 Ask for more: a quota request an administrator approves, with the reason and the decision recorded (P2)
- [ ] POR-202 Share within the workspace: hand an app, a key's scope or a dataset to a colleague, within what the workspace holds (P2)
- [ ] POR-203 Notifications: my job finished, my budget is nearly gone, my key is about to expire (P2)
