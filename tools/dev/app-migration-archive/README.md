# APP private migration source archive

This salvage-only directory preserves local prototypes, test drivers, generated
HTTP overlays, candidate SQL, patches, and the retired admission entry points.
It adds no supported product feature and must not be merged as an implementation.

Files ending in `.source` are complete, readable historical source projections.
They are data, have no executable permission, and must never be executed or used
to reactivate an old gate. The closed entries remain closed. No new epoch or
resource admission is configured here. Original files remain in place.

`manifest.json` maps every source to its original path and SHA-256, lists excluded
outputs and data, and records archive-member inspection. No dependency tree,
model, database, credential, compiled binary, Git bundle, or test output is added.
The build is NOTRUN because fresh raw free memory did not meet the full profile.

Original candidate DAGs are retained under the explicit salvage refs in the
manifest. The current APP component at 95f85dbb is already reachable from the
observed GitHub main. Reflog-only historical objects are reserved for the
coordinator's global preservation pass; this archive does not duplicate it.
