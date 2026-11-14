// Runs the help of one entrance on a real installation: the articles and the API reference are
// served by the installation itself, the saved contract is this entrance's only, and nothing is
// fetched from anywhere else. Skipped unless the environment describes an installation;
// docs/development/environment.md has the recipe.
import { expect, test } from '@playwright/test';
import { expectAccessible } from './accessibility-flows.ts';
import { adminPassword, email, origin } from './live.ts';

/**
 * Registers the live help flow for the entrance at `OWNSOLID_LIVE_ORIGIN`. The spec file applies
 * `liveOptions` and the skip at its top level, as Playwright requires for browser options.
 */
export function liveHelpFlows(audience: 'admin' | 'portal'): void {
  test.describe('help on an installation', () => {
    test('serves the articles and this entrance’s API reference from the installation', async ({
      page,
    }) => {
      test.setTimeout(120_000);
      const refused: string[] = [];
      const elsewhere: string[] = [];
      page.on('response', (response) => {
        const { pathname } = new URL(response.url());
        if (pathname.startsWith('/api/') && pathname !== '/api/v1/me' && response.status() >= 400) {
          refused.push(`${String(response.status())} ${pathname}`);
        }
      });
      page.on('request', (sent) => {
        if (new URL(sent.url()).origin !== origin) elsewhere.push(sent.url());
      });

      await page.goto('/account');
      await page.getByLabel('Email').fill(email);
      await page.getByLabel('Password').fill(adminPassword());
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByRole('heading', { name: 'Account', level: 1 }).waitFor();

      await page.getByRole('link', { name: 'Help for this page' }).click();
      await expect(page.getByRole('heading', { name: 'Your account', level: 1 })).toBeVisible();
      await expect(page.locator('#what-you-can-change')).toBeVisible();
      await expectAccessible(page);

      await page.goto('/help/api?q=changePassword#identity.changePassword');
      await expect(page.getByRole('heading', { name: 'Request body' })).toBeVisible();
      await expect(page.getByText('PASSWORD_CURRENT_INVALID')).toBeVisible();
      await expectAccessible(page);

      const download = page.waitForEvent('download');
      await page.getByRole('link', { name: 'Save the contract: identity' }).click();
      const saved = await (await download).createReadStream();
      const chunks: Buffer[] = [];
      for await (const chunk of saved) chunks.push(chunk as Buffer);
      const contract = JSON.parse(Buffer.concat(chunks).toString('utf8')) as {
        paths: Record<string, Record<string, { 'x-audiences'?: string[] }>>;
      };
      const operations = Object.values(contract.paths).flatMap((item) =>
        Object.entries(item).filter(([key]) => key !== 'parameters'),
      );
      expect(operations.length).toBeGreaterThan(0);
      for (const [, operation] of operations) {
        expect(operation['x-audiences']).toContain(audience);
      }

      expect(refused).toEqual([]);
      expect(elsewhere).toEqual([]);
    });
  });
}
