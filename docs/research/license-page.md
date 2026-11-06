# License page

Research behind DEL-116: the administrator's page for the license's standing, its import, and why a
file was refused.

## How others solve it

- **HashiCorp Nomad** ([`license inspect`](https://developer.hashicorp.com/nomad/commands/license/inspect)):
  inspects a file on disk, prints a "Validation Checks" block ending in "All checks passed", and
  exits non-zero with the failed check and the dates involved, such as "License ExpirationDate must
  be after Version BuildDate". Run before an upgrade, because a server will not start with a bad
  license.
- **HashiCorp Vault** ([`license inspect`](https://developer.hashicorp.com/vault/docs/commands/license/inspect)):
  prints the signature and format result, then issuer, customer, start, expiry and termination,
  ending in "License is valid".
- **GitLab** ([activate with a file](https://docs.gitlab.com/administration/license_file/)): the admin
  area shows the license; a banner appears 30 days before expiry; on expiry the instance turns
  read-only. A file is uploaded in the admin area, and renewals can only be added there.
- **Grafana Enterprise** ([licensing](https://grafana.com/docs/grafana/latest/administration/enterprise-licensing/)):
  one Stats and license page with usage and "Upload a new token". On expiry most of Grafana keeps
  working. Its refusal message names the cause, a license for a different root URL.
- **Elastic** ([manage your license](https://www.elastic.co/docs/deploy-manage/license/manage-your-license-in-self-managed-cluster)):
  a License Management page for status and upload of a JSON file; warnings start 30 days ahead; a
  license cannot be installed before its effective date; an update needs no restart.

The page shapes agree: one page, status, expiry, upload. Only HashiCorp shows why a file fails, and
only from a command line, which an offline site's administrator may not have to hand.

## Choices

- **Inspect, then import.** `POST /api/v1/admin/license/inspections` runs every check an import runs
  and changes nothing but the audit trail, as Nomad's command does, but in the page. A refusal is a
  200 with the checks, not a failure: the request worked. Inspection and import share `judge`, so
  they cannot disagree, and `wire.Inspect` backs `wire.Verify`.
- **Each check is reported in order,** and those after a failure as not checked. Passed checks tell a
  person what is already fine, which is what an issuer asks first.
- **Why, and the two values.** A file for another deployment shows both IDs; another issuer shows
  both key IDs; a wrong predecessor shows both license IDs; a renewal names the rule it broke (six
  in `renewalRefusal`). `LICENSE_ARTIFACT_INVALID` stays the one public import error, and the check
  says which of format, key, signature or terms failed.
- **Terms show only once proven.** A file that fails its signature shows no customer or dates: they
  are unsigned claims. The key ID it names is shown, marked by the failed key check.
- **The preview is the confirmation.** A license cannot be removed, so the design system's rule for
  irreversible actions applies, but typing a name would prove nothing: the person just chose the
  file. They see what it would do, give a reason, and press a button that names the action, with the
  permanence stated beside it. A wrong file cannot get this far: it must be signed for this
  installation and extend the license.
- **The server's clock decides.** Days left use `observed_at`, because a browser's clock may be wrong
  and the server's is the one that refuses work.
- **A renewal notice 60 days ahead.** GitLab and Elastic use 30, online. A signed file travels by hand
  to an offline site, and the prototype shows 58 days as a due renewal. Hidden once a renewal waits.
- **Limits are honest.** A license carries no node, user or feature limit (the product decision: one
  whole-platform license), so the page says so and shows what does limit it: whether new work is
  allowed, and imports used of `licensing.maximum_imports`, which the API now reports.
- **The first failure stays recoverable.** Inspection joins import in the list of actions a missing or
  expired license cannot block.

Rejected: a separate dialog, since the page has room and a refusal needs it; a file-picker-only
import with the reason on the error, which leaves the person guessing; a history table of accepted
licenses, which belongs with DEL-109's renewal and migration.

## Licences

No new dependency. Signing stays in `ownsolid-entitlement`; `wire.Inspect` only verifies.

## Failure modes and tests

- Each refusal, from a real signature to a bad file: `pkg/license` unit tests and `licensing` tests on
  PostgreSQL; every code and renewal rule in the browser flow; real signed files on a live install.
- Inspect and import disagreeing: `TestInspectionAgreesWithImportAndChangesNothing`.
- Unproven terms in the answer: `TestInspectionKeepsUnprovenTermsOffTheAnswer`.
- Another administrator importing first: the import is bound to the head shown, refused with
  `LICENSE_CURRENT_CHANGED`, and the page checks the file again; browser flow.
- A license that blocks its own remedy: the recovery list test, and an inspection with none installed.
- A failed inspection, import or download: a retry that keeps the file; browser flow.
- Empty or oversized files: refused in the browser without sending them; browser flow.
- A 64-character digest widening a card: a browser flow at three widths.

## What done means

From the navigation or the sidebar an administrator sees the license's state, expiry, grace and limits
and what to do next; copies the deployment ID; downloads diagnostics; chooses a file and sees every
check; for a refused file sees why and the values involved and downloads a report; imports a good one
with a reason, in both languages, with loading, empty, error and permission states.
