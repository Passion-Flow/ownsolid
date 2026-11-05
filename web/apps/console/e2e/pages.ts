import { failedMail, sentMail, type ControlPlane } from '@ownsolid/shell/testing';
import { shellPages, type PageState } from '@ownsolid/shell/testing/pages';
import type { Page } from '@playwright/test';

async function openMail(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/mail');
  await page.getByRole('heading', { name: 'Email delivery', level: 1 }).waitFor();
}

async function openSettings(page: Page, control: ControlPlane): Promise<void> {
  control.signedIn = true;
  await page.goto('/settings');
  await page.getByRole('heading', { name: 'Settings', level: 1 }).waitFor();
}

/**
 * Every page state of the console, checked by accessibility.spec.ts. A page this
 * application adds gets its states here; src/router.test.ts fails until it does.
 */
export const consolePages: readonly PageState[] = [
  ...shellPages,
  {
    name: 'settings',
    path: '/settings',
    open: async (page, control) => {
      await openSettings(page, control);
      await page.getByLabel('Default language').waitFor();
    },
  },
  {
    name: 'settings unavailable',
    path: '/settings',
    open: async (page, control) => {
      control.tenantSettingsProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openSettings(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'email delivery with a failed email',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, sentMail];
      await openMail(page, control);
      await page.getByRole('button', { name: /^Send again to/ }).waitFor();
    },
  },
  {
    name: 'email delivery empty',
    path: '/mail',
    open: async (page, control) => {
      await openMail(page, control);
      await page.getByText('No emails yet').waitFor();
    },
  },
  {
    name: 'email delivery unavailable',
    path: '/mail',
    open: async (page, control) => {
      control.mailProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
      await openMail(page, control);
      await page.getByRole('button', { name: 'Try again' }).waitFor();
    },
  },
  {
    name: 'email delivery with a refused resend',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail];
      control.nextResend = { status: 409, code: 'INVITATION_STATE_CONFLICT' };
      await openMail(page, control);
      await page.getByRole('button', { name: /^Send again to/ }).click();
      await page.getByText('The invitation changed').waitFor();
    },
  },
];
