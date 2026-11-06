import { useRouter, useRouterState, type RouterHistory } from '@tanstack/react-router';
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

/**
 * A dialog's open state that {@link useHashAction} also opens later, when the hash arrives on a
 * page already showing, as a shortcut pressed on that page does.
 */
export function useHashDialog(action: string): [boolean, (open: boolean) => void] {
  const router = useRouter();
  const hash = useRouterState({ select: (state) => state.location.hash });
  const [open, setOpen] = useState(useHashAction(action));
  useEffect(() => {
    if (hash !== action) return;
    setOpen(true);
    const { pathname, search } = (router.history as RouterHistory).location;
    void router.navigate({ href: `${pathname}${search}`, replace: true });
  }, [hash, action, router]);
  return [open, setOpen];
}
