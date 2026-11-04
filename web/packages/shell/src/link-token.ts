// Tokens from emailed links arrive in the URL fragment, which browsers never send to a
// server or put in a Referer header.
import { useEffect, useState } from 'react';

const tokenFormat = /^[A-Za-z0-9_-]{43}$/;

/** Reads a well-formed `#token=` value, or returns null. */
export function parseLinkToken(hash: string): string | null {
  const token = new URLSearchParams(hash.replace(/^#/, '')).get('token');
  return token !== null && tokenFormat.test(token) ? token : null;
}

function clearHash(): void {
  window.history.replaceState(window.history.state, '', window.location.pathname);
}

/**
 * Returns the token of the current link and removes it from the address bar and the
 * browser history, so it cannot be read back later or copied along with the address.
 * A link pasted into an open page changes only the fragment, so that is followed too.
 */
export function useLinkToken(): string | null {
  const [token, setToken] = useState(() => parseLinkToken(window.location.hash));
  useEffect(() => {
    if (window.location.hash !== '') clearHash();
    const follow = () => {
      if (window.location.hash === '') return;
      setToken(parseLinkToken(window.location.hash));
      clearHash();
    };
    window.addEventListener('hashchange', follow);
    return () => {
      window.removeEventListener('hashchange', follow);
    };
  }, []);
  return token;
}
