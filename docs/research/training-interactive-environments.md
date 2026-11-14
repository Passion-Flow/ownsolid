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


## Prepared wheel candidate and remaining closure checks

The [JupyterLab 4.6.6 release](https://pypi.org/project/jupyterlab/4.6.6/)
is the selected private prototype candidate. Its universal wheel is
17,334,944 bytes with SHA-256
4261910ccf1ecad6e0b3ca0debe96d4fde9735bfc9707509c125aab850e06b81.
PyPI binds it to upstream source
[c438ffaef710e6594c2e4f55d3c0eb1539142db5](https://github.com/jupyterlab/jupyterlab/tree/c438ffaef710e6594c2e4f55d3c0eb1539142db5).
The resolved candidate includes Jupyter Server 2.21.1 and ipykernel 7.4.0.
No package has been installed and no interactive server has been started.

Connected preparation staged 90 wheels, totaling 48,147,832 bytes, for
CPython 3.12 and Linux x86-64 with a maximum glibc tag of 2.36. Preparation
used host CPython 3.12.3 and a verified read-only pip 26.2.1 wheel; it did
not mutate the protected 37-package CPU training environment or the separate
one-package Arrow environment. A bounded streaming check matched every
wheel RECORD hash and size and archived 295 metadata, RECORD and notice
files. The candidate inventory retains every distribution/version, wheel
hash, original notice path/hash and native-library path.

The retained inputs are under
/root/lane-trn-jupyter-metadata.90mpy44y on the Linux server.
The current inventory-v3.json SHA-256 is
2c4f5fcbc429bf686dafa06f691c78365f4264156174b7a80c1fe89406befe7c.
That inventory is a private preparation artifact, not an admitted runtime
lock, complete SBOM, installed readback or production image.

Root wheel metadata must be distinguished from embedded dependency metadata:
bleach 6.4.0 also contains html5lib 1.1 metadata and its MIT notices.
JupyterLab carries frontend third-party licence JSON, jupyter-builder carries
Yarn/semver notices, debugpy carries bundled notices, and PyZMQ carries
libzmq/libsodium binaries and licences. An installed-distribution count does
not close those embedded graphs. Native component versions must come from
verified inputs/readback, not from a shared-library SONAME.

The wheel-bound frontend JSON contains 275 package entries, and the bundled
Yarn graph has 140. Their exact file hashes are
1c9066e41144ea261d4dc780f7d92dca55d5f33453f80e0a6009a63dd4673e07
and 64277dc79d080ff103f37f88cea4958f0f495d29180f427d57dd05d090a34512.
Bind each file to its distribution and full relative path; the basename alone
also matches other packages' different licence graphs.

The frontend includes elkjs 0.9.3 under
[EPL-2.0](https://www.eclipse.org/org/documents/epl-2.0/EPL-2.0.html),
which permits commercial/offline use and distribution subject to its source
availability and notice requirements. Preserve the corresponding source in
the offline distribution. Font Awesome Free 5.15.4 has distinct icon/font/code
terms, [CC-BY-4.0, OFL-1.1 and MIT](https://github.com/FortAwesome/Font-Awesome/blob/5.15.4/LICENSE.txt);
preserve their component-specific attribution. DOMPurify 3.4.16 offers
MPL-2.0 or Apache-2.0; the embedded file contains the Apache-2.0 text.

Khroma 2.1.0 has an empty generated licence ID and no licence field in its
package metadata. The [original npm archive](https://registry.npmjs.org/khroma/-/khroma-2.1.0.tgz)
was independently verified against its published SHA-512 integrity. Its
original MIT licence exactly matches the embedded text, SHA-256
66b333b0f66759a0b710459e03f7029abe17f4358114a128d2c972e642961b49.
An empty metadata field is not evidence that a package has no licence; keep
that verified original and explicit resolution in the closure inventory.

The project metadata includes MIT, MIT-0, BSD, ISC, Apache-2.0, PSF-2.0
and MPL-2.0 terms, including explicit combined/alternative expressions.
Retain the full original terms and notices rather than reducing a compound
expression to one convenient licence. The
[Mozilla MPL FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)
permits internal company use and explains the source/notice obligations for
redistributed executables and libraries. This candidate still needs the
matching embedded/native source and notice analysis, exact base-image
closure and installed byte readback before release admission. No model or
dataset is added.

For the eventual protocol probe, use the official
[Jupyter REST API](https://jupyter-server.readthedocs.io/en/stable/developers/rest-api.html),
[WebSocket negotiation](https://jupyter-server.readthedocs.io/en/stable/developers/websocket-protocols.html)
and [kernel message specification](https://jupyter-client.readthedocs.io/en/stable/messaging.html).
Kernel execution, file persistence, terminal I/O, isolated private homes,
credential-file handling and positive stop must run before a prototype is
called successful. The independent browser egress/revocation and shared
ownsolid lifecycle/identity/quota/Agent prerequisites above remain open.
