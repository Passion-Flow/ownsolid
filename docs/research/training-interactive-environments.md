# Isolated interactive training environments

Research for TRN-109, reviewed 2026-11-13. This is an integration boundary,
not an implemented environment or a production image admission.

## Problem and current owner contract

A user needs a private notebook, file editor and remote terminal without
modifying an admitted production training process, its runtime or its files.
The current training/Agent wire contract admits finite training.run.v1
attempts and accepted training outputs. It has no interactive-environment
operation, browser route or persistent-home authority. A notebook must not
be disguised as a long-running training attempt or inherit its credentials.
Agent operations, serving composition, identity and Web changes belong to
other lanes; this owner does not add those contracts unilaterally.

## Primary-source comparisons

[JupyterHub single-user servers](https://jupyterhub.readthedocs.io/en/6.2.0/explanation/singleuser.html)
separate a user's server process from Hub coordination and check OAuth server
access. Jupyter Server exposes IdentityProvider and Authorizer extension
points; their existence does not prove an ownsolid integration. The
[JupyterHub OAuth lifecycle](https://jupyterhub.readthedocs.io/en/6.2.0/explanation/oauth.html)
includes separate browser, server and upstream identity state. In particular,
expiring the Hub login cookie alone does not revoke an existing single-user
server session. ownsolid membership/session revocation therefore needs an
actual end-to-end browser and WebSocket test.

[JupyterHub's security guidance](https://jupyterhub.readthedocs.io/en/latest/explanation/websecurity.html)
explains the limits of a shared browser origin when users can run arbitrary
code or modify their server environment. Keep user-authored content apart
from Console/Portal and other users. Do not relax authentication or embedding
protections merely to put an editor inside a platform page. Exact private
origins, certificate trust and DNS remain typed installation inputs.

[Kubeflow Notebooks](https://www.kubeflow.org/docs/components/notebooks/overview/)
uses separately managed development environments, centrally provided images
and resource access control. Its
[container-image contract](https://www.kubeflow.org/docs/components/notebooks/container-images/)
separates Jupyter and other web IDEs from the workloads they develop. This
is evidence for the separation pattern; ownsolid does not gain a Kubernetes
or Kubeflow dependency from that comparison.

## Selected next step and rejected shortcuts

Prove one CPU Linux amd64 JupyterLab path outside product code first: a
private persistent home, notebook execution, file editing and a terminal,
with the exact image and dependencies prepared for disconnected use.
Keep its process, writable directories, lifecycle and browser authority
separate from production training. JupyterHub is the authentication/lifecycle
reference, not a second identity authority admitted by this note. Choosing
an actual orchestrator/authenticator and production protocol requires the
owning Agent, identity and route contracts plus a passing prototype.

Reject launching Jupyter from train.py, giving users a Docker socket,
reusing a job's writable model/checkpoint mounts, putting a bearer token in
a browser URL, exposing an unauthenticated server behind a navigation link,
or installing packages/extensions from the internet at runtime. None proves
Workspace isolation, revocation or positive process termination. An SSH
port-forward is useful for an operator prototype, but is not the Portal flow.

## Offline defaults must be manufactured explicitly

The [JupyterLab announcement controls](https://jupyterlab.readthedocs.io/en/stable/user/announcements.html)
provide separate server and browser settings. The candidate offline profile
must disable the announcements frontend plugin and select the documented
jupyterlab.NeverCheckForUpdate server class. News are opt-in upstream;
the product profile must still remove external news/update paths rather
than depending on an individual browser's remembered choice.

The [extension manager](https://jupyterlab.readthedocs.io/en/stable/user/extensions.html)
defaults to the PyPI implementation. Select the offline listing behavior
and preserve only packaged, reviewed extensions. Upstream's
[ListModel contract](https://jupyterlab.readthedocs.io/en/latest/api/classes/extensionmanager.ListModel-1.html)
also permits external metadata such as author avatars after consent,
including with a read-only manager. Selecting readonly alone is therefore
insufficient evidence of zero external browser requests. Freeze the relevant
plugins/settings and verify a browser network trace, including reconnect,
help, math rendering and opening extension controls, with egress denied.
The server installation and configuration remain read-only to user kernels.

The [server configuration reference](https://jupyter-server.readthedocs.io/en/stable/other/full-config.html)
states that custom_display_url can append an enabled token automatically.
It is not a redaction control. Before the prototype runs, prove that generated
credentials enter through private files and appear in no command, URL,
server banner or retained log. Authentication and CSRF remain enforced.
Record browser traffic, effective settings, original notices and the
closed artifact inventory alongside the prototype outcome. These settings
have been researched; no Jupyter process or dependency is installed here.

## Candidate licences and release closure

| Candidate or comparison | Original licence | Commercial, redistribution and offline implications |
| --- | --- | --- |
| [JupyterLab](https://raw.githubusercontent.com/jupyterlab/jupyterlab/main/LICENSE) | BSD-3-Clause | Commercial and offline use and redistribution are permitted with the original notices, conditions and disclaimer; no endorsement right. Candidate UI, not installed here. |
| [Jupyter Server](https://raw.githubusercontent.com/jupyter-server/jupyter_server/main/LICENSE) | BSD-3-Clause | Same notice and non-endorsement obligations. Candidate server, not installed here. |
| [JupyterHub](https://raw.githubusercontent.com/jupyterhub/jupyterhub/main/LICENSE) | BSD-3-Clause | Same obligations. Authentication/lifecycle comparison; no Hub dependency is added. |
| [Kubeflow Notebooks](https://raw.githubusercontent.com/kubeflow/notebooks/main/LICENSE) | Apache-2.0 | Commercial and offline redistribution is permitted subject to licence, modification and applicable notice obligations. Comparison only; no controller or image is copied. |

These project licences do not qualify any wheelhouse, base image, Node
closure, kernel, extension or optional IDE. Before a prototype is staged,
pin every selected artifact and preserve each dependency's original licence.
No production version/digest, model, dataset or download is introduced here.
Use generated CC0 notebook cells for the eventual test, not customer content.

## Required failure and acceptance coverage

- From Portal, an authorized Workspace member creates the CPU environment,
  opens a notebook and terminal, edits a file, stops and reopens it, and reads
  the persisted file. UI loading, empty, failure, retry and denied states work
  in English and Simplified Chinese without an external route.
- Each create/start/reopen operation repeats current identity, Workspace,
  quota and local execution scope. Replayed or uncertain writes resolve the
  same persisted environment; an unauthenticated identifier grants no access.
- A different user/Workspace/tenant cannot open files, kernels or terminals.
  Session revocation and removal/regrant invalidate old browser access and
  existing WebSockets within the admitted lifecycle contract. A stale page
  cannot regain access through a retained server cookie.
- The environment cannot mutate the production runtime, model, dataset,
  checkpoint or Agent credentials. A job submitted from it still uses normal
  authorized training admission and immutable inputs.
- Retry after port conflict, disk exhaustion, missing/changed image and a
  failed spawn creates no duplicate process or leaked resource reservation.
  Stop and idle cleanup release resources only after an exact positive stop;
  lost contact remains visibly unresolved and cannot kill an unrelated process.
- Verify dependency installation, first launch, static assets, kernels,
  extensions, reconnect and restart with egress denied. Record CPU acceptance
  separately from GPU/device acceptance. No real GPU is required for the
  first notebook/editor/terminal path.

## Boundary

The separation pattern is established upstream. The ownsolid lifecycle,
browser authority, revocation, persistent-home schema/backup, quota and Agent
protocol integration are not proven. Do not add an interface shell or tick
TRN-109. The next executable work is the isolated CPU prototype after its
pinned closure and an admitted resource window exist, then the owning shared
contracts; the current lane can continue other eligible training/data work.
