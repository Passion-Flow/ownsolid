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

The server checks every file against its published hash before publishing the model. When the import
is ready, choose **Open the model** to see the published record.

## If an import fails

A failed import says why. **Verify again** retries it a limited number of times. **Discard** deletes
what was uploaded. A workspace takes one import at a time, so a failed import must be finished or
discarded before the next can start.

## Retiring a model

**Deploy a model** on a model's page opens [Model services](help:model-services) with the model chosen.

**Retire** on a model's page takes it out of the list and stops new deployments of it. A model still
in use cannot be retired: stop what depends on it first. Its files stay in the store and its history
stays in the audit trail.

## The model store

Models live in `data/models` under the installation's root. During import, allow space for both the
uploaded bundle and the unpacked model. The form estimates the total from the selected bundle
and the model's published file sizes.

After publication, the server removes the bundle and temporary verification files in the
background and retries if needed. The import history and published model files remain available.
The import page shows publication status. If storage stays in use, an administrator can look for
`model_import_cleanup_failed` and the import ID in the control service logs.
[Backups](help:getting-started) carry published models.
