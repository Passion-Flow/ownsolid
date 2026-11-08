// What a screen-reader or keyboard user needs when the page changes without a document
// load: the tab names the page, and focus moves to the new page's heading.
import { useRouter, useRouterState } from '@tanstack/react-router';
import { useEffect, useRef } from 'react';

const product = 'ownsolid';

/** Sets the browser tab to the page's name, which tabs, history and screen readers read. */
export function useDocumentTitle(title: string | undefined): void {
  useEffect(() => {
    document.title = title ? `${title} - ${product}` : product;
  }, [title]);
}

/**
 * Moves focus to the new page's main heading after each navigation to another path, so a
 * screen reader announces the page and the next Tab starts inside it. The first load
 * keeps the browser's own focus, and a change of only the query or hash keeps focus
 * where it is, so filters and drawers stay usable.
 */
export function usePageFocus(): void {
  const router = useRouter();
  const previousPath = useRef(router.state.location.pathname);
  const pathname = useRouterState({ select: (state) => state.resolvedLocation?.pathname });
  useEffect(() => {
    // A navigation can finish before the first location has been resolved.
    if (!pathname || pathname === previousPath.current) return;
    previousPath.current = pathname;
    const target = document.querySelector<HTMLElement>('main h1') ?? document.querySelector('main');
    if (!target) return;
    if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1');
    target.focus();
  }, [pathname]);
}
