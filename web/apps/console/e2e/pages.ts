import {
  failedMail,
  secondFailedMail,
  sentMail,
  type ControlPlane,
  type ScriptedView,
} from '@ownsolid/shell/testing';
import { shellPages, type PageState } from '@ownsolid/shell/testing/pages';
import type { Page } from '@playwright/test';

/** A saved view that shows only failed emails. */
export const failedView: ScriptedView = {
  id: '6f1c7a1e-0000-4000-8000-000000000020',
  list: 'mail-deliveries',
  name: 'Failures',
  state: { filters: { state: 'failed' }, hiddenColumns: [] },
  revision: 1,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
};

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
    name: 'email delivery with rows selected',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, secondFailedMail, sentMail];
      await openMail(page, control);
      await page.getByRole('checkbox', { name: 'Select grace@example.test' }).check();
      await page.getByRole('toolbar', { name: 'Actions for the selected rows' }).waitFor();
    },
  },
  {
    name: 'email delivery details',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail];
      await openMail(page, control);
      await page.getByRole('button', { name: /^Show details of the email to / }).click();
      await page.getByRole('dialog', { name: failedMail.recipient_email }).waitFor();
    },
  },
  {
    name: 'email delivery columns menu',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [sentMail];
      await openMail(page, control);
      await page.getByRole('button', { name: 'Choose the columns to show' }).click();
      await page.getByRole('menuitemcheckbox', { name: 'Attempts' }).waitFor();
    },
  },
  {
    name: 'email delivery views menu with a changed view',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [failedMail, sentMail];
      control.savedViews = [failedView];
      await openMail(page, control);
      await page.getByRole('button', { name: 'Views: Default view' }).click();
      await page.getByRole('menuitem', { name: 'Failures' }).click();
      await page
        .getByRole('group', { name: 'Show emails' })
        .getByRole('button', { name: 'All' })
        .click();
      await page.getByRole('button', { name: /^Views: Failures, Unsaved/ }).click();
      await page.getByRole('menuitem', { name: 'Save the changes' }).waitFor();
    },
  },
  {
    name: 'email delivery save view dialog',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [sentMail];
      await openMail(page, control);
      await page.getByRole('button', { name: 'Views: Default view' }).click();
      await page.getByRole('menuitem', { name: 'Save as a new view' }).click();
      await page.getByRole('dialog', { name: 'Save this view' }).waitFor();
    },
  },
  {
    name: 'email delivery with no matches',
    path: '/mail',
    open: async (page, control) => {
      control.mail = [sentMail];
      await openMail(page, control);
      await page.getByLabel('Filter by recipient').fill('nobody');
      await page.getByText('No emails match').waitFor();
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
