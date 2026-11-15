---
name: machine-reset-2026-11-15
description: "On 2026-11-15 the Linux build server was wiped to a fresh install and the MacBook was reinstalled; where the only surviving copies of unbacked work went"
metadata:
  node_type: memory
  type: project
---

On 2026-11-15 both development machines were reset: the Linux build server
`ownsolid-cpu` was wiped back to a fresh-install state, and the MacBook was
reinstalled straight afterwards. See [[dev-environment]] for what the machines
were before this.

**The server.** 498 unpushed commits were resolved first. Of 60 branches holding
unpushed work, 39 held commits not already on `main`; those were pushed to GitHub
as `salvage/cpu2/*` and verified tip by tip against `git ls-remote`, 0
discrepancies. The server had no GitHub credentials, so the push ran from the Mac
with the server added as a remote over SSH. Afterwards: Docker and all images,
volumes and containers removed and uninstalled; `/root` and `/home/appadmin`
emptied except shell config and `.ssh`; Go, Node, Rust and PostgreSQL clients
uninstalled; logs, journal, apt cache and histories cleared. Disk went from 232 GB
to 12 GB of 1005 GB. **Tailscale was removed too**, at the user's instruction that
all tunnels would be rebuilt, so the server is no longer reachable at
100.100.110.24. It now answers only on its LAN address as `appadmin@10.200.0.4`
(password, in the sudo group) or at its console. Its stale Tailscale device record
may still be listed in the admin console.

**The MacBook.** All 300 local branches across the six GitHub-backed repositories
(`ownsolid`, `ownsolid-demo`, `ownsolid-entitlement`, `project-one`, `project-two`,
`Claude Guard`) were verified contained in remote branches: nothing was unpushed.
What was not in any remote went to
`Development/mac-preinstall-backup-20261115/` (76 MB, with a README and
`CHECKSUMS.sha256`): the `openrelay` history bundle, `project-demo`, three
`ownsolid` stashes, `project-three`'s video, this memory directory, and
`settings.json`. **If a later session cannot find that directory, the user did not
copy it off before reinstalling, and its contents are gone.**

**Two things that looked like losses and were not.** A project called `yatoly` was
found on the server, 30 GB under `/home/appadmin/.yb0-8083-1102/`, with local
`file://` remotes only. It belongs to `hanap <hanap@hanapdeMacBook-Air.local>`, not
to this user, who did not recognise it; the original is on that person's own
machine, and the rescued archive was deleted on the user's instruction. And
`project-openrelay/openrelay-src` has no `origin`, only an `upstream` pointing at
`QuantumNous/new-api`, which it is a fork of — but its code is on GitHub at
`Passion-Flow/openrelay`, branch `review-snapshot-2026-11-09`, whose tree hash
matches the local `or/integration-launch` exactly. Only the 737 locally authored
commits are absent there.

`Development/project-demo` is the Novatra-era demo from 2026-11-03, Python and
plain JavaScript, with no git at all. `ownsolid-demo` replaced it on 2026-11-05 as
a fresh repository sharing no content; its commit `ab358eb` records that the
novatra-demo was stopped with its files kept for rollback. That is why it has no
history to push. See [[sales-demo-build]].
