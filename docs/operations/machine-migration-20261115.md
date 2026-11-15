# Moving development to another machine

Written 2026-11-15, from a full survey of the repository, GitHub and both
machines. Read this first on the new computer.

## 1. Where the work is

`main` is healthy and holds almost everything. It has 1,142 commits, 914 of them
added in the seven days to 2026-11-13, and 89 of 589 roadmap items are ticked
(P0 closed at 28/28, P1 at 61/326).

`make build` passes on `main`. `make check` passes too, with one condition in
section 3.

## 2. The branches on GitHub, and what they are

There are **1,214 branches**. Only one of them, `main`, is the product.

| Kind | Count | What it is |
| --- | ---: | --- |
| `main` | 1 | The product |
| `salvage/*/history/<sha>` | 888 | One branch per commit, pushed as a safety copy |
| `salvage/<machine>/<lane>/…` | 325 | Lane snapshots, same purpose |

They exist because a migration instruction said to keep a salvage branch rather
than risk losing a line of code, and the lanes applied it to every commit. No
data was lost; the repository is simply very noisy.

**How much of it is redundant**, measured rather than assumed:

| | Already in `main` | Not in `main` |
| --- | ---: | ---: |
| `history/*` branches (888) | **765** | 123 |
| Named salvage branches (325) | **214** | 111 |

The 765 and 214 are pre-rebase copies: the same change is on `main` under a
different hash, because the lane rebased before pushing. They are safe to delete
once section 5 is done.

Of the 123 history branches not on `main`:

- **59 are `git stash` leftovers** (`On <branch>: …`, `autostash`) — not commits
- **4 are `fixup!`/`squash!`** — superseded by what they fixed
- **60 look like real commits** and need the review in section 5

Together with the 111 named branches, **about 171 branches hold something that is
not demonstrably on `main`.** That is the set worth a person's attention; the
other ~1,040 are copies.

## 3. `make check` fails on a Mac with the stock Python

`tools/codegen/sdk/generate.py` uses `match`/`case`, which needs **Python 3.10 or
newer**. macOS ships Python 3.9.6 with Xcode, and `make check` picks it up and
reports twelve import errors that look like broken code. They are not.

```bash
python3 --version          # 3.10+ required
PYTHON=python3.12 make check   # passes: 48 tests OK
```

Install Python 3.12 or later on the new machine and make sure `python3` resolves
to it, or always pass `PYTHON=`. `ownsolid-cpu` already has 3.12.3 and is fine.

## 4. What is not in Git, and must be carried by hand

Cloning the repository is not enough. None of this is in version control:

| Thing | Where it lives | Without it |
| --- | --- | --- |
| `~/.ssh/ownsolid_cpu_ed25519` | the old Mac | no access to the Linux dev server |
| `~/.ssh/ownsolid_demo_ed25519` | the old Mac | no access to the demo server |
| `~/.ssh/config` | the old Mac | the `ownsolid-cpu` host and its Tailscale proxy are undefined |
| Tailscale login | the old Mac | `100.100.110.24` is unreachable |
| Demo sign-in details | `/etc/ownsolid-demo.env` on the demo server | nothing lost; read them there |
| Toolchains | — | Go, Rust, pnpm, Docker, Python 3.12 all reinstall |

**The SSH config is the one that is easy to forget and breaks the most.**

## 5. Before deleting anything

498 commits on `ownsolid-cpu` have not been pushed, across 62 branches and 35
worktrees. Those are at real risk, because a new machine will not see them.

In order:

1. Push what the server still holds, or confirm each branch is already on `main`.
2. Review the ~171 branches from section 2 and either merge them or record why
   not.
3. Only then delete the redundant branches.

Nothing here is urgent as long as nobody deletes a worktree or reinstalls the old
Mac. Deleting branches is the last step, never the first.

## 6. Worktrees

68 on the Mac, 35 on the server. They are scratch space and do not need to move;
the new machine makes its own. Do not delete the old ones until section 5 is
done.
