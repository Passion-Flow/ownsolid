// Route-level code splitting. A page's code loads with its route, through
// `lazyRouteComponent`, which also reloads the document once when a newer deployment
// has replaced the page's file.
import type { AsyncRouteComponent } from '@tanstack/react-router';
import type { FunctionComponent } from 'react';

/**
 * Gives a route component the preload of the lazy components it renders, so the router
 * fetches their code while the route loads, or when a link to it is hovered, instead of
 * after the route first renders.
 */
export function preloading<TProps>(
  component: FunctionComponent<TProps>,
  ...parts: readonly AsyncRouteComponent<never>[]
): AsyncRouteComponent<TProps> {
  return Object.assign(component, {
    preload: async () => {
      // A part that has loaded has no preload left.
      await Promise.all(parts.map((part) => part.preload?.() ?? Promise.resolve()));
    },
  });
}
