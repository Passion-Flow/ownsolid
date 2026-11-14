// The address a single sign-on round trip returns to. Kept apart from the result page so
// the route can validate it without loading the page.

/** Where a sign-in or an account link came back to. */
export interface SsoResultSearch {
  result?: string;
  error?: string;
}

/** Keeps only what the result page reads; the values come from the address. */
export function parseSsoResult(search: Record<string, unknown>): SsoResultSearch {
  return {
    ...(typeof search.result === 'string' ? { result: search.result } : {}),
    ...(typeof search.error === 'string' ? { error: search.error } : {}),
  };
}
