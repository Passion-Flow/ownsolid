// What is supported, the same in both entrances: an option's state, why it cannot be chosen and
// what would change that, from the product's own declaration.
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import { declaredCapabilities, mockControlPlane, type ControlPlane } from './control-plane.ts';

const planned = (page: Page) => page.getByRole('button', { name: /^Planned/ });

/** The flows of the page, for one entrance. */
export function supportFlows(audience: ControlPlane['audience']): void {
  test.describe('what is supported', () => {
    let control: ControlPlane;

    test.beforeEach(async ({ page }) => {
      control = await mockControlPlane(page, audience);
      control.signedIn = true;
    });

    test('reaches the page from the navigation', async ({ page }) => {
      await page.goto('/');
      const group = audience === 'admin' ? 'System' : 'Workspace';
      await page.getByRole('button', { name: group, exact: true }).click();
      await page.getByRole('navigation').getByRole('link', { name: 'What is supported' }).click();
      await expect(
        page.getByRole('heading', { name: 'What is supported', level: 1 }),
      ).toBeFocused();
    });

    test('shows every registry and option from the declaration', async ({ page }) => {
      await page.goto('/support');
      for (const registry of declaredCapabilities().registries) {
        await expect(page.getByRole('heading', { name: registry.name, level: 2 })).toBeVisible();
        const table = page.getByRole('table', { name: registry.name });
        await expect(table.locator('tbody tr')).toHaveCount(registry.options.length);
      }
      const nvidia = page.getByRole('row', { name: /^NVIDIA/ });
      await expect(nvidia).toContainText('Planned');
      await expect(nvidia).toContainText('still being built');
      await expect(nvidia).toContainText('GPU-101');
      const macos = page.getByRole('row', { name: /^macOS/ });
      await expect(macos).toContainText('Not supported');
      await expect(macos).toContainText('development machine');
    });

    test('filters by state from a tile, and a link keeps the filter', async ({ page }) => {
      await page.goto('/support');
      await planned(page).click();
      await expect(page).toHaveURL(/state=planned/);
      await expect(planned(page)).toHaveAttribute('aria-pressed', 'true');
      await expect(page.getByRole('row', { name: /^NVIDIA/ })).toBeVisible();
      await expect(page.getByRole('row', { name: /^macOS/ })).toHaveCount(0);
      await page.reload();
      await expect(planned(page)).toHaveAttribute('aria-pressed', 'true');
      await expect(page.getByRole('row', { name: /^macOS/ })).toHaveCount(0);
    });

    test('finds an option by name or roadmap item, and hides registries without one', async ({
      page,
    }) => {
      await page.goto('/support');
      await page.getByRole('searchbox', { name: 'Search options' }).fill('gpu-204');
      await expect(page.getByRole('row', { name: /^Huawei Ascend/ })).toBeVisible();
      await expect(page.getByRole('row', { name: /^NVIDIA/ })).toHaveCount(0);
      await expect(page.getByRole('heading', { name: 'Serving engines' })).toHaveCount(0);
      await expect(page).toHaveURL(/q=gpu-204/);
    });

    test('says when nothing matches, and clears the filters', async ({ page }) => {
      await page.goto('/support?q=nothing-has-this-name&state=planned');
      await expect(page.getByRole('heading', { name: 'No option matches' })).toBeVisible();
      await page.getByRole('button', { name: 'Clear filters' }).click();
      await expect(page.getByRole('row', { name: /^macOS/ })).toBeVisible();
      await expect(page.getByRole('searchbox', { name: 'Search options' })).toHaveValue('');
      await expect(page).not.toHaveURL(/state=/);
    });

    test('a state with no options takes no tile, and one with options does', async ({ page }) => {
      await page.goto('/support');
      await expect(planned(page)).toBeVisible();
      await expect(page.getByRole('button', { name: /^Needs your licence/ })).toHaveCount(0);
    });

    test('a licensed option says what it needs', async ({ page }) => {
      control.capabilities = {
        registries: declaredCapabilities().registries.map((registry) => ({
          ...registry,
          options: registry.options.map((option) =>
            option.id === 'macos'
              ? { ...option, state: 'licensed' as const, roadmap_item: 'CMP-218' }
              : option,
          ),
        })),
      };
      await page.goto('/support?state=licensed');
      const macos = page.getByRole('row', { name: /^macOS/ });
      await expect(macos).toContainText('Needs your licence');
      await expect(macos).toContainText('CMP-218');
      await expect(page.getByRole('row', { name: /^NVIDIA/ })).toHaveCount(0);
    });

    test('a failed read explains itself and retries', async ({ page }) => {
      control.capabilitiesProblem = { status: 503, code: 'SERVICE_NOT_READY' };
      await page.goto('/support');
      const retry = page.getByRole('button', { name: 'Try again' });
      await expect(retry).toBeVisible();
      control.capabilitiesProblem = null;
      await retry.click();
      await expect(page.getByRole('heading', { name: 'Accelerators', level: 2 })).toBeVisible();
    });

    test('reads in Simplified Chinese', async ({ page }) => {
      control.preferences.locale = 'zh-CN';
      await page.goto('/support');
      await expect(
        page.getByRole('heading', { name: chinese['capability.page.title'], level: 1 }),
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { name: chinese['capability.accelerators.title'], level: 2 }),
      ).toBeVisible();
      await expect(page.getByRole('main')).toContainText(
        chinese['capability.node-operating-systems.macos.reason'],
      );
    });
  });
}
