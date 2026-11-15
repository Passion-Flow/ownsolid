# Claude's project memory, saved 2026-11-15

An orphan branch: it carries no product code, only these notes. It exists because
this memory lived under `~/.claude/projects/` on a MacBook that was about to be
reinstalled, and nothing else would have survived that.

`memory/MEMORY.md` is the index loaded at the start of a session; each other file
holds one fact. To restore them on a new machine:

```bash
git fetch origin salvage/claude-memory-20261115
git archive origin/salvage/claude-memory-20261115 memory | \
  tar x -C ~/.claude/projects/<the project directory>/
```

These files record what was true on 2026-11-15. `machine-reset-2026-11-15.md`
describes the state both machines were left in. Anything naming a file, a flag or
a server address should be checked against the repository before being relied on.
