import { missingHelpPages } from '@ownsolid/shell/testing/help';
import { describe, expect, it } from 'vitest';
import { portalPages } from '../e2e/pages.ts';
import { router } from './router.tsx';

describe('router', () => {
  it('has an accessibility check for every page and none for a page it lacks', () => {
    const routes = Object.keys(router.routesByPath).sort();
    const checked = [...new Set(portalPages.map((state) => state.path))]
      .filter((path) => path !== '*')
      .sort();
    expect(checked).toEqual(routes);
  });

  it('has every page its help explains or links to', async () => {
    expect(await missingHelpPages('portal', Object.keys(router.routesByPath))).toEqual([]);
  });
});
