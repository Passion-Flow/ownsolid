# Model library

The [Model library](/library) holds the models of the workspace chosen in the switcher, and shows
how they arrive. A model arrives as a bundle, so importing one needs no network.

## Importing a model

1. On a connected machine, fetch the model's files at the revision shown under **Catalogue**.
2. Open [Import a bundle](/library/import) and expand **How to make a bundle**. Copy the command
   for your tool and run it in the folder with the files for that revision.
3. Bring the resulting `bundle.tar` to a computer that can reach the console and choose the file.
4. Give a reason, accept the model's licence and choose **Start import**.

The upload continues while you use the rest of the console. Closing the tab stops it: open the
import from **Imports**, choose the same file, and it continues from where the server stopped. A
different file is refused before any of it is stored.

The server checks every file against its published hash before publishing the model. When the import
is ready, choose **Open the model** to see the published record.

## Making a bundle

Use an installed tool on the machine that holds the model files. The commands on the import page
list the exact filenames for the approved revision.

- **Python 3** writes an uncompressed archive using `tarfile.USTAR_FORMAT`.
- **GNU tar on macOS** uses the `gtar` command if GNU tar is installed there.
- **GNU tar as `tar`** applies only when `tar --version` reports GNU tar.

The built-in macOS tar writes headers the importer rejects, even with `--format=ustar`. Each
command packs only the listed files. Upload `bundle.tar` as written, with no compression or extra
entries.

## If an import fails

A failed import says why. **Verify again** retries it a limited number of times. **Discard** deletes
what was uploaded. A workspace takes one import at a time, so a failed import must be finished or
discarded before the next can start.

## Expired uploads

If an unfinished upload expires, its status explains what happened. **Expired** means its temporary files have been removed and it cannot resume. Choose **Upload a new bundle**, select the bundle again and start a new import. While cleanup is running, wait for it to finish. Import history keeps the original byte count.

For scripts, use a new `Idempotency-Key` for the fresh import. Reusing the old key returns the original expired import; it does not create another upload.

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
