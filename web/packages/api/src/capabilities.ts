// Every option the product offers and whether this release can do it (ADR 0036, ADR 0042).
import { createClient } from './client.ts';
import type { capabilitiesContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

/** The whole declaration, in display order. */
export type CapabilityDeclaration =
  capabilitiesContract.components['schemas']['CapabilityDeclaration'];

/** One family of options, such as the accelerators. */
export type CapabilityRegistry = capabilitiesContract.components['schemas']['CapabilityRegistry'];

/** One choice in a registry. */
export type CapabilityOption = capabilitiesContract.components['schemas']['CapabilityOption'];

/** Whether an option can be chosen in this release. */
export type CapabilityState = CapabilityOption['state'];

const client = createClient<capabilitiesContract.paths>();

/** Reads the declaration. It is fixed for a release, so one read serves a whole visit. */
export function listCapabilities(options?: RequestOptions): Promise<CapabilityDeclaration> {
  return client.get('/api/v1/capabilities', options);
}
