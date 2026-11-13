# DEL private source preservation

This is a standalone salvage archive, not a product branch or an accepted feature.
Do not merge this root history into main and do not run the archived files.
Every source copy has a `.source` suffix and Git mode 100644. Old scripts contain
retired gate, server, cleanup and transfer commands; they are preserved as data only.
No old lock, gate, queue or service was executed during this preservation.

The source manifest maps 168 original private source, test, patch, documentation
and static fixture files to their copies. 166 copies are byte-for-byte originals.
Two explicit derivative copies omit embedded binary Git bundle payloads. Their
original local files remain untouched, with original SHA-256, computed Git blob ID,
bundle SHA-256 and header recorded. The bundled commits are reachable through the
immutable remote refs in `original-ref-mapping.json`. No binary bundle, dependency,
model, database, runtime log, credential or private key is included here.

The retired Mac gate JSON keeps six historical script sources as non-executable
data. This archive grants no permission to revive those gates. Current sealed
exit-78 stubs are also preserved. Draft command-cancellation, image-inventory and
TLS qualification sources are not evidence that their unrun cases passed.

The original e3aff53eaeae247011215cdaa37e4940fa20a02a DAG and recovered acceptance
history were pushed and checked on 55 immutable salvage refs before rebase. The
mapping covers original DEL branches, custom refs and owned historical/reflog
inputs. It preserves original OBS author commits and published ADR identities.
The private recovery index has 4906 entries, all backed by preserved history or
the original GitHub main; its binary index stays local.

The clean migration candidate is 305cc970aa61e9f367d64e8046b60333cedea126.
Rebase with merges and diff whitespace checks passed. Full make check/build did
not run: the authorized carrier identity matched, but raw free memory was
1247395840 bytes, below the required 6442450944 bytes. Main was not advanced.
No roadmap checkbox was added by this migration. Native Linux TLS, actual image
CLI, installed acceptance, captured offline egress, upgrade rollback and the full
21-item P1 objective remain subject to their existing recorded qualification gaps.

Local execution receipts remain outside Git under the coordinator migration DEL
directory. Originals and existing worktrees were retained without modification.
