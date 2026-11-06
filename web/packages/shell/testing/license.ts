// A scripted platform license for browser flows.
import type {
  License,
  LicenseCheck,
  LicenseFileTerms,
  LicenseInspection,
  LicenseSummary,
} from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const deployment = '6f1c7a1e-0000-4000-8000-0000000000d0';
const fingerprint = 'a'.repeat(64);

/** A license in force, with months left. The mock's clock stands at `observed_at`. */
export const activeSummary: LicenseSummary = {
  license_id: '6f1c7a1e-0000-4000-8000-0000000000d1',
  artifact_sha256: 'b'.repeat(64),
  customer_reference: 'Northwind Compute',
  not_before: '2026-01-01T00:00:00Z',
  expires_at: '2027-11-01T00:00:00Z',
  grace_until: '2027-12-01T00:00:00Z',
};

/** An installation with `activeSummary` in force. */
export const activeLicense: License = {
  state: 'active',
  new_business_allowed: true,
  warning: false,
  deployment_id: deployment,
  current: activeSummary,
  pending: null,
  accepted_licenses: 1,
  history_head_license_id: activeSummary.license_id,
  maximum_imports: 64,
  observed_at: '2026-11-07T08:00:00Z',
  public_key_fingerprint: fingerprint,
};

/** An installation with no license yet. */
export const missingLicense: License = {
  ...activeLicense,
  state: 'missing',
  new_business_allowed: false,
  warning: true,
  current: null,
  accepted_licenses: 0,
  history_head_license_id: '',
};

/** The terms of a renewal of `activeSummary`, in force at once. */
export const renewalTerms: LicenseFileTerms = {
  ...activeSummary,
  license_id: '6f1c7a1e-0000-4000-8000-0000000000d2',
  artifact_sha256: 'c'.repeat(64),
  not_before: '2026-11-01T00:00:00Z',
  expires_at: '2028-11-01T00:00:00Z',
  grace_until: '2028-12-01T00:00:00Z',
  deployment_id: deployment,
  issued_at: '2026-10-30T00:00:00Z',
  replaces_license_id: activeSummary.license_id,
};

const checkNames: LicenseCheck['name'][] = [
  'format',
  'key',
  'signature',
  'terms',
  'deployment',
  'identity',
  'capacity',
  'renewal',
];

/** Check results with `failed` refused and the ones after it not run. */
export function checksStoppingAt(failed?: LicenseCheck['name']): LicenseCheck[] {
  const stop = failed ? checkNames.indexOf(failed) : checkNames.length;
  return checkNames.map((name, index) => ({
    name,
    result: index < stop ? 'passed' : index === stop ? 'failed' : 'not_checked',
  }));
}

/** An inspection of a file that can be imported. */
export const importableInspection: LicenseInspection = {
  outcome: 'importable',
  code: '',
  detail: '',
  checks: checksStoppingAt(),
  key_id: 'issuer-2026',
  trusted_key_id: 'issuer-2026',
  file: renewalTerms,
  state: activeLicense,
};

/** What a flow changes to script the license. */
export interface LicenseScript {
  license: License;
  licenseProblem: { status: number; code: string } | null;
  /** What the next inspections answer. */
  inspection: LicenseInspection;
  /** The next inspection fails like this. */
  nextInspection: { status: number; code: string } | null;
  /** The next import fails like this. */
  nextLicenseImport: { status: number; code: string } | null;
  /** The next diagnostics download fails like this. */
  nextDiagnostics: { status: number; code: string } | null;
  /** Files sent for inspection, in order. */
  inspectedFiles: string[];
  /** Imports received, in order. */
  licenseImports: { artifact: string; reason: string; expected_current_license_id: string }[];
}

/** The state of a mock with nothing scripted. */
export function emptyLicenseScript(): LicenseScript {
  return {
    license: activeLicense,
    licenseProblem: null,
    inspection: importableInspection,
    nextInspection: null,
    nextLicenseImport: null,
    nextDiagnostics: null,
    inspectedFiles: [],
    licenseImports: [],
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

type Scripted = 'nextInspection' | 'nextLicenseImport' | 'nextDiagnostics';

function take(state: LicenseScript, name: Scripted) {
  const scripted = state[name];
  state[name] = null;
  return scripted;
}

/** Serves the license routes from `state`. */
export async function routeLicense(
  page: Page,
  state: LicenseScript & { audience: string },
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const refused = (route: Route) =>
    state.audience !== 'admin'
      ? { status: 404, code: 'RESOURCE_NOT_FOUND' }
      : route.request().headers()['x-csrf-token'] !== 'csrf-token'
        ? { status: 403, code: 'CSRF_INVALID' }
        : null;
  await page.route('**/api/v1/license/status', (route) =>
    data(route, {
      state: state.license.state,
      new_business_allowed: state.license.new_business_allowed,
      warning: state.license.warning,
    }),
  );
  await page.route('**/api/v1/admin/license', (route) => {
    const request = route.request();
    if (request.method() === 'GET') {
      if (state.audience !== 'admin') {
        return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      }
      if (state.licenseProblem) return problem(route, state.licenseProblem);
      return data(route, state.license);
    }
    const refusal = refused(route) ?? take(state, 'nextLicenseImport');
    if (refusal) return problem(route, refusal);
    state.licenseImports.push(request.postDataJSON() as LicenseScript['licenseImports'][number]);
    const file = state.inspection.file ?? renewalTerms;
    state.license = {
      ...activeLicense,
      current: file,
      accepted_licenses: state.license.accepted_licenses + 1,
      history_head_license_id: file.license_id,
    };
    return data(route, {
      state: state.license,
      imported_license_id: file.license_id,
      repeated: false,
    });
  });
  await page.route('**/api/v1/admin/license/inspections', (route) => {
    const refusal = refused(route) ?? take(state, 'nextInspection');
    if (refusal) return problem(route, refusal);
    const { artifact } = route.request().postDataJSON() as { artifact: string };
    state.inspectedFiles.push(artifact);
    return data(route, { ...state.inspection, state: state.license });
  });
  await page.route('**/api/v1/admin/license/diagnostics', (route) => {
    const refusal = take(state, 'nextDiagnostics');
    if (refusal) return problem(route, refusal);
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      headers: { 'Content-Disposition': 'attachment; filename=license-diagnostics.json' },
      body: JSON.stringify({ format_version: 1, state: state.license }),
    });
  });
}
