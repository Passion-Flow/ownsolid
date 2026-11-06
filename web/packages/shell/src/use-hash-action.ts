import { useRouter, type RouterHistory } from '@tanstack/react-router';
import { useEffect, useState } from 'react';

/**
 * True when a link opened the page for `action`, such as `/nodes#add` from a quick action. The
 * hash is cleared at once, so a reload or Back does not repeat the action.
 */
export function useHashAction(action: string): boolean {
  const router = useRouter();
  const history = router.history as RouterHistory;
  const [requested] = useState(() => history.location.hash === `#${action}`);
  useEffect(() => {
    if (!requested) return;
    const { pathname, search } = history.location;
    void router.navigate({ href: `${pathname}${search}`, replace: true });
  }, [requested, router, history]);
  return requested;
}
