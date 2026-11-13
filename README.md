# DEV Mac migration source archive

This salvage branch preserves private ownsolid prototypes, protocol fixtures, and verification drivers from the Mac development lane. It is not a product release and makes no new roadmap completion claim. Original product commits are preserved on the salvage branches mapped in `migration/refs.json`.

The scripts are historical evidence. Some encode retired admission policies, old machine paths, or superseded process controls. Do not execute them as current gates. Re-establish the current environment and admission policy before any future validation.

Only source, necessary configuration, and migration mappings are included. Dependencies, generated clients, model/data/database files, binaries, bundles, logs, temporary outputs, and credentials are excluded without deleting their originals. Product source copies already covered by original Git history are not duplicated.

`migration/source-manifest.json` records every archived source file and its SHA-256. `migration/exclusions.json` records excluded paths or directory scopes. `migration/verification.json` distinguishes prior measured evidence from commands not run during migration.
