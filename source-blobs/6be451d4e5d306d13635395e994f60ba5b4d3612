// The platform license, for platform administrators (api/openapi/v1/licensing).
import { createClient } from './client.ts';
import type { licensingContract } from './gen/index.ts';
import { getFile, type DownloadedFile, type RequestOptions } from './transport.ts';

type Schemas = licensingContract.components['schemas'];

/** The installation's license, read from its signed history. */
export type License = Schemas['LicenseState'];

/** One accepted license. */
export type LicenseSummary = Schemas['LicenseSummary'];

/** What importing a file would do and, when it would be refused, why. */
export type LicenseInspection = Schemas['LicenseInspection'];

/** One check a file went through. */
export type LicenseCheck = Schemas['LicenseCheck'];

/** The signed terms of an inspected file. */
export type LicenseFileTerms = Schemas['LicenseFileTerms'];

/** The largest license file the server reads (wire.MaximumArtifactBytes). */
export const maximumLicenseFileBytes = 65_536;

/**
 * How many days before expiry the console asks for a renewal. A signed file travels by hand to an
 * offline site, so this is twice the 30 days common online.
 */
export const renewalNoticeDays = 60;

const dayMilliseconds = 86_400_000;

const client = createClient<licensingContract.paths>();

/** Whole days from the server's clock to `moment`, rounded up. The browser's clock may be wrong. */
export function licenseDaysUntil(moment: string, license: License): number {
  return Math.ceil((Date.parse(moment) - Date.parse(license.observed_at)) / dayMilliseconds);
}

/** Reads the license and how this installation stands. */
export function getLicense(options?: RequestOptions): Promise<License> {
  return client.get('/api/v1/admin/license', options);
}

/** Runs every check an import runs. Nothing changes, and a refusal is an answer, not a failure. */
export function inspectLicense(
  artifact: string,
  options?: RequestOptions,
): Promise<LicenseInspection> {
  return client.post('/api/v1/admin/license/inspections', { ...options, body: { artifact } });
}

/**
 * Imports a license file. `expectedHead` binds the import to the history the person looked at, so
 * it is refused if another administrator imported first. Importing the same file twice is safe.
 */
export function importLicense(
  input: { artifact: string; reason: string; expectedHead: string },
  options?: RequestOptions,
): Promise<Schemas['LicenseImportResult']> {
  const { artifact, reason, expectedHead } = input;
  return client.post('/api/v1/admin/license', {
    ...options,
    body: { artifact, reason, expected_current_license_id: expectedHead },
  });
}

/** Downloads the audited description of the license, to send to the issuer. */
export function downloadLicenseDiagnostics(options?: RequestOptions): Promise<DownloadedFile> {
  return getFile('/api/v1/admin/license/diagnostics', options);
}
