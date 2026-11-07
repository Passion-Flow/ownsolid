# Model library

The [Model library](/library) holds the models of the workspace chosen in the switcher, and shows
how they arrive. A model arrives as a bundle, so importing one needs no network.

## Importing a model

1. On a connected machine, fetch the model's files at the revision shown under **Catalogue**.
2. In the folder that holds them, make the bundle. The page shows the exact command under **How to
   make a bundle**.
3. Bring the file to a computer that can reach the console, choose **Import a bundle** and pick it.
4. Give a reason, accept the model's licence and choose **Start import**.

The upload continues while you use the rest of the console. Closing the tab stops it: open the
import from **Imports**, choose the same file, and it continues from where the server stopped. A
different file is refused before any of it is stored.

Then every file is checked against its published hash. Nothing is kept unless all of them match.

## If an import fails

A failed import says why. **Verify again** retries it a limited number of times. **Discard** deletes
what was uploaded. A workspace takes one import at a time, so a failed import must be finished or
discarded before the next can start.

## Retiring a model

**Retire** on a model's page takes it out of the list and stops new deployments of it. A model still
in use cannot be retired: stop what depends on it first. Its files stay in the store and its history
stays in the audit trail.

## The model store

Models live in `data/models` under the installation's root. An import holds the bundle and the
unpacked model at once, so the store needs room for both while it runs. [Backups](help:getting-started)
carry published models.
