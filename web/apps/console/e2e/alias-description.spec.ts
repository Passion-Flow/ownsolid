// S-205 description edits: independent revisions, retained drafts and safe exact retries.
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import {
  deploymentAlias,
  deploymentKey,
  expectAccessible,
  expectFocusVisible,
  mockControlPlane,
  node,
  readyModel,
  runningDeployment,
  type ControlPlane,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;
let external: string[];
const initial = 'For support requests.\nReview answers before sending.';

function currentAlias() {
  const alias = control.aliases[0];
  if (!alias) throw new Error('The fixture alias is missing');
  return alias;
}

test.beforeEach(async ({ page, baseURL }) => {
  external = [];
  if (!baseURL) throw new Error('The preview origin is required');
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (/^https?:$/.test(url.protocol) && url.origin !== new URL(baseURL).origin) {
      external.push(request.url());
    }
  });
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.nodes = [node];
  control.models = [readyModel];
  control.deployments = [runningDeployment];
  control.gatewayKeys = [deploymentKey];
  control.aliases = [{ ...deploymentAlias, description: initial, description_revision: 1 }];
  await page.emulateMedia({ reducedMotion: 'reduce' });
});

test.afterEach(() => {
  expect(external).toEqual([]);
});

for (const [locale, text] of [
  ['en', english],
  ['zh-CN', chinese],
] as const) {
  test.describe(locale, () => {
    test.use({ locale });
    const key = (name: string) => text[`services.gateway.description.${name}` as keyof typeof text];
    const card = (page: Page) =>
      page.getByRole('region', { name: text['services.gateway.title'], exact: true });
    async function open(page: Page) {
      await page.goto(`/services/${runningDeployment.id}`);
      await card(page)
        .getByRole('button', { name: key('edit'), exact: true })
        .click();
      return page.getByRole('dialog', { name: key('edit'), exact: true });
    }

    test('saves, reloads, clears and undoes without changing routing or keys', async ({
      page,
    }, info) => {
      if (locale === 'zh-CN') await page.setViewportSize({ width: 390, height: 844 });
      const dialog = await open(page);
      await expect(dialog.getByLabel(key('label'), { exact: true })).toHaveValue(initial);
      await dialog
        .getByLabel(key('label'), { exact: true })
        .fill('Answer support questions.\nAsk a person about refunds.');
      await dialog
        .getByLabel(text['gateway.reason'], { exact: true })
        .fill('Clarify the intended use');
      await expectAccessible(page);
      await expectFocusVisible(page);
      await info.attach(`console-description-edit-${locale}`, {
        body: await page.screenshot({ animations: 'disabled' }),
        contentType: 'image/png',
      });
      await dialog.getByRole('button', { name: key('save') }).press('Enter');
      await expect(dialog).toHaveCount(0);
      await expect(card(page)).toContainText('Ask a person about refunds.');
      await page.reload();
      const edit = card(page).getByRole('button', { name: key('edit'), exact: true });
      await edit.focus();
      await edit.press('Enter');
      await page.getByLabel(key('label'), { exact: true }).fill('');
      await page
        .getByLabel(text['gateway.reason'], { exact: true })
        .fill('Remove outdated guidance');
      await page.getByRole('button', { name: key('save') }).click();
      await expect(card(page).getByText(key('empty'), { exact: true })).toBeVisible();
      await page.getByRole('button', { name: text['action.undo'], exact: true }).click();
      await expect(card(page)).toContainText('Ask a person about refunds.');
      expect(
        control.publishWrites.map((write) => write.body.expected_description_revision),
      ).toEqual([1, 2, 3]);
      expect(control.aliases[0]?.revision).toBe(deploymentAlias.revision);
      expect(control.aliases[0]?.deployment_id).toBe(deploymentAlias.deployment_id);
      expect(control.gatewayKeys).toEqual([deploymentKey]);
    });

    test('keeps a conflicting draft through failed review, then saves against reviewed text', async ({
      page,
    }, info) => {
      if (locale === 'zh-CN') await page.setViewportSize({ width: 390, height: 844 });
      const dialog = await open(page);
      const draft = dialog.getByLabel(key('label'), { exact: true });
      await draft.fill('My retained draft');
      await dialog
        .getByLabel(text['gateway.reason'], { exact: true })
        .fill('Explain current limits');
      control.aliases = [
        {
          ...currentAlias(),
          description: 'Another administrator wrote this.',
          description_revision: 2,
        },
      ];
      await dialog.getByRole('button', { name: key('save') }).click();
      await expect(dialog.getByText(key('conflict'), { exact: true })).toBeVisible();
      await expect(dialog.getByRole('button', { name: text['action.reload'] })).toHaveCount(0);
      await expect(draft).toHaveValue('My retained draft');
      await expect(dialog.getByRole('button', { name: key('save') })).toHaveAttribute(
        'aria-disabled',
        'true',
      );
      let refuse = true;
      await page.route('**/admin/distribution/workspaces/*/aliases?*', (route) =>
        refuse ? route.abort('connectionreset') : route.fallback(),
      );
      await dialog.getByRole('button', { name: key('review') }).click();
      await expect(dialog.getByRole('button', { name: text['action.retry'] })).toBeVisible();
      await expect(draft).toHaveValue('My retained draft');
      refuse = false;
      await dialog.getByRole('button', { name: text['action.retry'] }).click();
      await expect(
        dialog.getByText('Another administrator wrote this.', { exact: true }),
      ).toBeVisible();
      await expect(dialog.getByLabel(text['gateway.reason'], { exact: true })).toHaveValue(
        'Explain current limits',
      );
      await expectAccessible(page);
      await info.attach(`console-description-conflict-${locale}`, {
        body: await page.screenshot({ animations: 'disabled' }),
        contentType: 'image/png',
      });
      await dialog.getByRole('button', { name: key('save') }).click();
      await expect(dialog).toHaveCount(0);
      expect(
        control.publishWrites.map((write) => write.body.expected_description_revision),
      ).toEqual([1, 2]);
      expect(control.publishWrites[0]?.key).not.toBe(control.publishWrites[1]?.key);
      await expect(card(page)).toContainText('My retained draft');
    });

    test('keeps an unavailable predecessor option visible and closes with focus restored', async ({
      page,
    }) => {
      const dialog = await open(page);
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect(
        card(page).getByRole('button', { name: key('edit'), exact: true }),
      ).toBeFocused();
      control.aliases = [deploymentAlias];
      await page.reload();
      const edit = card(page).getByRole('button', { name: key('edit'), exact: true });
      await expect(edit).toHaveAttribute('aria-disabled', 'true');
      await expect(edit).toHaveAccessibleDescription(key('notReady'));
      await edit.press('Enter');
      await expect(page.getByRole('dialog')).toHaveCount(0);
    });

    for (const [status, code] of [
      [403, 'ACCESS_DENIED'],
      [404, 'MODEL_ALIAS_NOT_FOUND'],
    ] as const) {
      test(`retains readable input after ${status} and sends no further write`, async ({
        page,
      }) => {
        const dialog = await open(page);
        control.publishFailures = [{ status, code }];
        await dialog.getByLabel(key('label'), { exact: true }).fill('Keep this draft');
        await dialog.getByLabel(text['gateway.reason'], { exact: true }).fill('Review use');
        const save = dialog.getByRole('button', { name: key('save') });
        await save.click();
        await expect(dialog.getByText(text[`problem.${code}.title`])).toBeVisible();
        await expect(dialog.getByLabel(key('label'), { exact: true })).toHaveValue(
          'Keep this draft',
        );
        await expect(save).toHaveAttribute('aria-disabled', 'true');
        await save.press('Enter');
        expect(control.publishWrites).toHaveLength(1);
        expect(control.aliases[0]?.description).toBe(initial);
      });
    }
  });
}

test('retries a lost answer with the same intent and displays a later writer without Undo', async ({
  page,
}) => {
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'Edit description', exact: true }).click();
  control.publishFailures = ['lost'];
  await page.getByLabel('Description', { exact: true }).fill('');
  await page.getByLabel('Reason', { exact: true }).fill('Clear obsolete text');
  await page.getByRole('button', { name: 'Save description' }).click();
  await expect(page.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
  expect(control.aliases[0]?.description).toBe('');
  control.aliases = [{ ...currentAlias(), description: 'Newer guidance', description_revision: 3 }];
  await page.getByRole('button', { name: 'Try again', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(
    page.getByText('Your edit was applied. A newer description is now shown.'),
  ).toBeVisible();
  await expect(page.getByRole('region', { name: 'On the gateway', exact: true })).toContainText(
    'Newer guidance',
  );
  await expect(page.getByRole('button', { name: 'Undo', exact: true })).toHaveCount(0);
  expect(control.publishWrites[0]?.key).toBe(control.publishWrites[1]?.key);
  expect(control.aliases[0]?.description_revision).toBe(3);
});

test('accepts 500 supplementary characters and permits an edit while the model is paused', async ({
  page,
}) => {
  control.aliases = [
    { ...currentAlias(), available: false, unavailable_code: 'MODEL_ALIAS_UNAVAILABLE' },
  ];
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'Edit description', exact: true }).click();
  const field = page.getByLabel('Description', { exact: true });
  await field.fill('\u{1f680}'.repeat(501));
  await page.getByLabel('Reason', { exact: true }).fill('Describe supported use');
  await page.getByRole('button', { name: 'Save description' }).press('Enter');
  expect(control.publishWrites).toHaveLength(0);
  await expect(field).toHaveAttribute('aria-invalid', 'true');
  await field.fill(`  ${'\u{1f680}'.repeat(500)}\n`);
  await expect(page.getByText('500 / 500 characters', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Save description' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(control.publishWrites[0]?.body.description).toBe('\u{1f680}'.repeat(500));
  expect(control.aliases[0]?.available).toBe(false);
  expect(control.aliases[0]?.revision).toBe(deploymentAlias.revision);
});

test('keeps an unknown-outcome payload fixed until the same intent is answered', async ({
  page,
}) => {
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'Edit description', exact: true }).click();
  control.publishFailures = [{ status: 409, code: 'STORAGE_COMMIT_OUTCOME_UNKNOWN' }];
  const field = page.getByLabel('Description', { exact: true });
  await field.fill('Check this exact edit');
  await page.getByLabel('Reason', { exact: true }).fill('Explain the limits');
  await page.getByRole('button', { name: 'Save description' }).click();
  await expect(field).toHaveAttribute('readonly', '');
  await expect(page.getByRole('button', { name: 'Reload page' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Try again', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(control.publishWrites[0]?.key).toBe(control.publishWrites[1]?.key);
  expect(control.publishWrites[0]?.body).toEqual(control.publishWrites[1]?.body);
});

test('Undo refuses a later writer and keeps that writer visible', async ({ page }) => {
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'Edit description', exact: true }).click();
  await page.getByLabel('Description', { exact: true }).fill('');
  await page.getByLabel('Reason', { exact: true }).fill('Clear the description');
  await page.getByRole('button', { name: 'Save description' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  control.aliases = [
    { ...currentAlias(), description: 'Keep the newer text', description_revision: 3 },
  ];
  await page.getByRole('button', { name: 'Undo', exact: true }).click();
  await expect(page.getByText(english['action.undoFailed'], { exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: 'On the gateway', exact: true })).toContainText(
    'Keep the newer text',
  );
  expect(control.aliases[0]?.description_revision).toBe(3);
});

test('an answer after closing the editor cannot close the next dialog', async ({ page }) => {
  let release: () => void = () => {
    throw new Error('The response gate is not initialized');
  };
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  let received: () => void = () => {
    throw new Error('The request gate is not initialized');
  };
  const requestReceived = new Promise<void>((resolve) => {
    received = resolve;
  });
  await page.route('**/aliases/*/description', async (route) => {
    received();
    await released;
    await route.fallback();
  });
  await page.goto(`/services/${runningDeployment.id}`);
  await page.getByRole('button', { name: 'Edit description', exact: true }).click();
  await page.getByLabel('Description', { exact: true }).fill('An edit finishing later');
  await page.getByLabel('Reason', { exact: true }).fill('Clarify the use');
  await page.getByRole('button', { name: 'Save description' }).click();
  await requestReceived;
  await page.keyboard.press('Escape');
  await page
    .getByRole('region', { name: 'On the gateway', exact: true })
    .getByRole('button', { name: 'Issue a key', exact: true })
    .click();
  const response = page.waitForResponse(
    (reply) => reply.url().endsWith('/description') && reply.ok(),
  );
  release();
  await response;
  await expect(page.getByRole('dialog', { name: 'Issue a key', exact: true })).toBeVisible();
  await expect(page.getByText('Description saved.', { exact: true })).toHaveCount(0);
});
