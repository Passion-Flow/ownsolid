// Public runtime configuration the control plane serves at /runtime-config.json.
// The same build runs in every installation; this is where it learns its policy.
import { ApiProblem } from './problem.ts';
import { setRequestTimeout } from './transport.ts';

/** Interface timing and paging policy chosen by the installation. */
export interface UiPolicy {
  /** How often live views refresh. */
  pollMilliseconds: number;
  /** Delay between typing in a search box and searching. */
  searchDebounceMilliseconds: number;
  /** Rows per page in lists. */
  pageSize: number;
  /** Rows per page in pickers and lookups. */
  lookupPageSize: number;
}

/** How long gateway keys live, chosen by the installation. */
export interface GatewayKeyPolicy {
  /** The lifetime of a key issued without one. */
  defaultTtlSeconds: number;
  /** The longest lifetime a key may ask for. */
  maximumTtlSeconds: number;
}

/** The validated subset of the runtime configuration the web interface uses. */
export interface RuntimeConfig {
  /** Installed topology size, for example `single-node`. */
  topologySize: string;
  /** True when the installation serves plain HTTP (development only). */
  plaintext: boolean;
  /** Per-request timeout. */
  requestTimeoutMilliseconds: number;
  /** The language of people who have not chosen one; `auto` leaves it to the browser. */
  defaultLocale: string;
  ui: UiPolicy;
  gatewayKeys: GatewayKeyPolicy;
}

function integer(value: unknown, minimum: number, maximum: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < minimum || value > maximum) {
    throw new ApiProblem('RESPONSE_INVALID');
  }
  return value;
}

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new ApiProblem('RESPONSE_INVALID');
  }
  return value as Record<string, unknown>;
}

// The server's bounds for key lifetimes (services/control/internal/platform/config/policy.go).
const minimumKeyTtlSeconds = 300;
const maximumKeyTtlSeconds = 90 * 24 * 60 * 60;

function gatewayKeyPolicy(keys: Record<string, unknown>): GatewayKeyPolicy {
  const maximumTtlSeconds = integer(
    keys.maximum_ttl_seconds,
    minimumKeyTtlSeconds,
    maximumKeyTtlSeconds,
  );
  return {
    defaultTtlSeconds: integer(keys.default_ttl_seconds, minimumKeyTtlSeconds, maximumTtlSeconds),
    maximumTtlSeconds,
  };
}

/**
 * Validates a runtime configuration document. Bounds match the control plane's
 * configuration validation, so a value outside them means a broken or foreign server.
 */
export function parseRuntimeConfig(document: unknown): RuntimeConfig {
  const data = record(record(document).data);
  const topology = record(data.topology);
  const transport = record(data.transport);
  const ui = record(data.ui);
  const keys = record(data.gateway_keys);
  if (typeof topology.size !== 'string' || topology.size === '')
    throw new ApiProblem('RESPONSE_INVALID');
  if (typeof transport.plaintext !== 'boolean') throw new ApiProblem('RESPONSE_INVALID');
  if (typeof data.default_locale !== 'string') throw new ApiProblem('RESPONSE_INVALID');
  return {
    topologySize: topology.size,
    plaintext: transport.plaintext,
    requestTimeoutMilliseconds: integer(data.request_timeout_milliseconds, 1_000, 300_000),
    defaultLocale: data.default_locale,
    ui: {
      pollMilliseconds: integer(ui.poll_milliseconds, 1_000, 60_000),
      searchDebounceMilliseconds: integer(ui.search_debounce_milliseconds, 0, 2_000),
      pageSize: integer(ui.page_size, 1, 100),
      lookupPageSize: integer(ui.lookup_page_size, 1, 100),
    },
    gatewayKeys: gatewayKeyPolicy(keys),
  };
}

/**
 * Fetches and validates the runtime configuration and applies its request timeout
 * to every later API call. Call once before the application renders.
 */
export async function loadRuntimeConfig(signal?: AbortSignal): Promise<RuntimeConfig> {
  let response: Response;
  try {
    response = await fetch('/runtime-config.json', {
      credentials: 'same-origin',
      cache: 'no-store',
      ...(signal ? { signal } : {}),
    });
  } catch {
    throw new ApiProblem('NETWORK_UNAVAILABLE');
  }
  if (!response.ok) throw new ApiProblem('SERVICE_NOT_READY', { status: response.status });
  let document: unknown;
  try {
    document = await response.json();
  } catch {
    throw new ApiProblem('RESPONSE_INVALID', { status: response.status });
  }
  const config = parseRuntimeConfig(document);
  setRequestTimeout(config.requestTimeoutMilliseconds);
  return config;
}
