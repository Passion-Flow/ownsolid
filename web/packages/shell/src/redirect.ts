// Where to go after sign-in. Only paths inside this application are accepted, so a
// crafted link cannot send a user to another site.

/** The sign-in path of every application. */
export const signInPath = '/sign-in';

/** The account page of every application. */
export const accountPath = '/account';

/**
 * Returns `target` when it is a path inside this application, otherwise `/`.
 * Rejects absolute URLs, protocol-relative `//host` paths, backslash tricks and
 * the sign-in page itself.
 */
export function safeRedirect(target: unknown): string {
  if (typeof target !== 'string' || !target.startsWith('/')) return '/';
  if (target.startsWith('//') || target.includes('\\')) return '/';
  // Control characters are dropped by URL parsers and could hide a host.
  for (const character of target) {
    const code = character.charCodeAt(0);
    if (code < 0x20 || code === 0x7f) return '/';
  }
  const url = new URL(target, 'http://application.invalid');
  if (url.origin !== 'http://application.invalid') return '/';
  if (url.pathname === signInPath) return '/';
  return `${url.pathname}${url.search}${url.hash}`;
}
