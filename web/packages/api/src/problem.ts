// Errors from the control-plane API and from the transport itself.

/** Codes the client raises when no usable answer arrived from the server. */
export const clientProblemCodes = [
  /** The request did not reach the server: network down, server stopped, DNS failure. */
  'NETWORK_UNAVAILABLE',
  /** No answer within the configured request timeout. */
  'REQUEST_TIMEOUT',
  /** The server answered with something that is not a valid API envelope. */
  'RESPONSE_INVALID',
] as const;

/** A client-side problem code. */
export type ClientProblemCode = (typeof clientProblemCodes)[number];

/** One rejected input field, as reported by the server. */
export interface FieldProblem {
  field: string;
  code: string;
}

/** Details carried by an {@link ApiProblem}. */
export interface ApiProblemInit {
  status?: number | undefined;
  requestId?: string | undefined;
  retryAfterSeconds?: number | undefined;
  fields?: readonly FieldProblem[] | undefined;
}

/**
 * A failed API call. `code` is the server's stable error code, such as
 * `AUTH_INVALID_CREDENTIALS`, or a {@link ClientProblemCode}. Interfaces map the
 * code to a translated message and show `requestId` so support can find the log.
 */
export class ApiProblem extends Error {
  /** Stable machine-readable code. */
  readonly code: string;
  /** HTTP status, or 0 when no response arrived. */
  readonly status: number;
  /** Server correlation ID, empty when no response arrived. */
  readonly requestId: string;
  /** Seconds the server asked the client to wait before retrying, when it said so. */
  readonly retryAfterSeconds: number | undefined;
  /** Rejected input fields, when the server named them. */
  readonly fields: readonly FieldProblem[];

  constructor(code: string, init: ApiProblemInit = {}) {
    super(code);
    this.name = 'ApiProblem';
    this.code = code;
    this.status = init.status ?? 0;
    this.requestId = init.requestId ?? '';
    this.retryAfterSeconds = init.retryAfterSeconds;
    this.fields = init.fields ?? [];
  }

  /** True when retrying the same request later may succeed. */
  get retryable(): boolean {
    return (
      this.status === 0 ||
      this.status === 429 ||
      this.status === 502 ||
      this.status === 503 ||
      this.status === 504
    );
  }
}

/** Returns true when the error is an {@link ApiProblem} with one of the codes. */
export function isProblem(error: unknown, ...codes: string[]): error is ApiProblem {
  return error instanceof ApiProblem && (codes.length === 0 || codes.includes(error.code));
}
