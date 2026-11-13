import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { mockControlPlane, node, readyModel, runningDeployment } from '@ownsolid/shell/testing';
import { expect, test } from '@playwright/test';

for (const [locale, messages] of [['en', english], ['zh-CN', chinese]] as const) {
  for (const width of [1440, 390]) {
    test(`detail actions preserve commands and one activity primary in ${locale} at ${String(width)}px`, async ({ page }, info) => {
      const control = await mockControlPlane(page, 'admin');
      control.signedIn = true;
      control.preferences.locale = locale;
      control.nodes = [node];
      control.models = [readyModel];
      control.deployments = [runningDeployment];
      await page.setViewportSize({ width, height: 900 });
      const more = () => page.getByRole('button', { name: messages['resource.actions'] });
      const capture = async (name: string) => {
        await info.attach(`detail-actions-${name}-${locale}-${String(width)}`, {
          body: await page.screenshot({ fullPage: true, animations: 'disabled' }),
          contentType: 'image/png',
        });
      };
      for (const [kind, path] of [
        ['node', `/nodes/${node.id}`],
        ['model', `/library/${readyModel.id}`],
        ['deployment', `/services/${runningDeployment.id}`],
      ]) {
        await page.goto(`${path}?tab=activity`);
        await expect(page.getByRole('tab', { name: messages['resource.tab.activity'], exact: true })).toHaveAttribute('aria-selected', 'true');
        await expect(page.getByRole('main').locator('.bg-accent-strong')).toHaveCount(1);
        await expect(page.getByRole('main').locator('.bg-accent-strong')).toHaveText(messages['resource.activity.read']);
        await capture(kind ?? 'resource');
      }
      await more().focus();
      await page.keyboard.press('Enter');
      const scale = page.getByRole('menuitem', { name: messages['services.scale.open'], exact: true });
      await expect(scale).toHaveAttribute('aria-disabled', 'true');
      await expect(scale).toHaveAccessibleDescription(messages['services.scale.notReady']);
      await scale.focus();
      await page.keyboard.press('Enter');
      expect(control.actions).toEqual([]);
      await expect(page.getByRole('menu')).toBeVisible();
      await capture('deployment-menu');
      await page.keyboard.press('Escape');
      await expect(more()).toBeFocused();
      await page.goto(`/nodes/${node.id}?tab=activity`);
      await more().focus();
      await page.keyboard.press('Enter');
      const rename = page.getByRole('menuitem', { name: messages['nodes.rename.open'] });
      await expect(rename).toBeFocused();
      await capture('node-menu');
      await page.keyboard.press('Enter');
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      await expect(dialog.locator(':focus')).toHaveCount(1);
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect(more()).toBeFocused();
    });
  }
}
