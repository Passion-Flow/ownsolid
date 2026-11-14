// Runs the Users page on a real installation: an invitation goes out through a real mail server, the
// person accepts it, the administrator disables the new account, which ends its session, and enables it
// again. Skipped unless the environment names a way to read the mail; docs/development/environment.md
// has the recipe.
import { expectAccessible } from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';
import { execSync } from 'node:child_process';
import { adminPassword, email, liveOptions, origin } from '@ownsolid/shell/testing/live';

// Prints every message the mail server received, one after another.
const readMail = process.env.OWNSOLID_LIVE_MAIL_COMMAND ?? '';

test.skip(origin === '' || readMail === '', 'needs a mail server the test can read');
test.use(liveOptions);

/** A message with its base64 parts decoded, as the outbox sends them. */
function decoded(message: string): string {
  return message.replace(
    /Content-Transfer-Encoding: base64\r?\n(?:.+\r?\n)*\r?\n([A-Za-z0-9+/=\r\n]+)/g,
    // The line break keeps a decoded part apart from the boundary after it.
    (_, body: string) => `${Buffer.from(body.replace(/\s/g, ''), 'base64').toString('utf8')}\n`,
  );
}

/** The newest invitation link sent to `address`, waiting for it to arrive. */
async function invitationLink(address: string, after: string | null): Promise<string> {
  let link = '';
  await expect
    .poll(
      () => {
        const mail = execSync(readMail, { encoding: 'utf8', timeout: 30_000 });
        const found = mail
          .split(/^To: /m)
          .filter((message) => message.startsWith(address))
          .map(decoded)
          .flatMap((message) => [
            ...message.matchAll(/https:\/\/[^\s"<>]+\/register#token=[\w-]{43}(?![\w-])/g),
          ])
          .map((match) => match[0]);
        link = found.at(-1) ?? '';
        return link !== '' && link !== after;
      },
      { timeout: 120_000, intervals: [2_000] },
    )
    .toBe(true);
  return link;
}

const signIn = async (page: Page, address: string, password: string) => {
  await page.getByLabel('Email').fill(address);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
};

test('invites someone who joins, then disables and enables the account', async ({
  page,
  browser,
}) => {
  test.setTimeout(300_000);
  const stamp = String(Date.now());
  const invitee = `live-${stamp}@lane-b.test`;
  const spare = `spare-${stamp}@lane-b.test`;
  const password = `Live users ${stamp} correct horse`;
  const name = `Person ${stamp}`;

  await page.goto('/users');
  await signIn(page, email, adminPassword());
  await expect(page.getByRole('heading', { name: 'Users', level: 1 })).toBeVisible();
  await expectAccessible(page);

  // Invite, and send it again before it is used: the second link replaces the first.
  await page.getByRole('button', { name: 'Invite', exact: true }).click();
  await page.getByLabel('Email address').fill(invitee);
  await page.getByRole('button', { name: 'Send the invitation' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: `Invitation sent to ${invitee}` }),
  ).toBeVisible();
  const invitations = page.getByRole('region', { name: 'Invitations' });
  await expect(invitations).toContainText(invitee);
  const first = await invitationLink(invitee, null);
  await invitations.getByRole('button', { name: `Send again ${invitee}` }).click();
  await expect(
    page.getByRole('status').filter({ hasText: `Invitation sent again to ${invitee}` }),
  ).toBeVisible();
  const second = await invitationLink(invitee, first);

  // The person accepts in a browser of their own.
  const theirs = await browser.newContext(liveOptions);
  // Each email link opens a tab of its own, as a mail client does.
  const stale = await theirs.newPage();
  await stale.goto(first);
  await expect(stale.getByText('This invitation cannot be used')).toBeVisible();
  await stale.close();
  const person = await theirs.newPage();
  await person.goto(second);
  await person.getByLabel('Your name').fill(name);
  await person.getByLabel('New password').fill(password);
  await person.getByLabel('Confirm the password').fill(password);
  await person.getByRole('button', { name: 'Create the account' }).click();
  await expect(person.getByText('Your account is ready')).toBeVisible({ timeout: 30_000 });
  await person.getByRole('link', { name: 'Sign in' }).click();
  await signIn(person, invitee, password);
  await expect(person.getByRole('heading', { name: /^Welcome, / })).toBeVisible();

  // The account is listed, found by its address, and its invitation is gone.
  await page.reload();
  await expect(invitations.getByText(invitee)).toHaveCount(0);
  await page.getByLabel('Filter by email').fill(`live-${stamp}`);
  const row = page.getByRole('row', { name: new RegExp(name) });
  await expect(row).toContainText('Active');
  await expect(page.getByRole('row')).toHaveCount(2);

  // Disabling ends the person's session at once.
  await page.getByRole('button', { name: `Open details of ${name}` }).click();
  await page.getByRole('dialog', { name }).getByRole('button', { name: 'Disable' }).click();
  const disable = page.getByRole('dialog', { name: `Disable ${invitee}` });
  await disable.getByLabel('Reason').fill('Live check of the Users page');
  await expectAccessible(page);
  await disable.getByRole('button', { name: 'Disable the account' }).click();
  await expect(row).toContainText('Disabled');
  await person.reload();
  await expect(person.getByRole('button', { name: 'Sign in' })).toBeVisible();
  await signIn(person, invitee, password);
  await expect(person.getByRole('heading', { name: /^Welcome, / })).toHaveCount(0);

  // Enabled again, the person signs in.
  await page.getByRole('button', { name: `Open details of ${name}` }).click();
  await page.getByRole('dialog', { name }).getByRole('button', { name: 'Enable' }).click();
  await expect(row).toContainText('Active');
  await person.goto(new URL(second).origin);
  await signIn(person, invitee, password);
  await expect(person.getByRole('heading', { name: /^Welcome, / })).toBeVisible();
  await theirs.close();

  // A second invitation is revoked by typing its address.
  await page.getByRole('button', { name: 'Invite', exact: true }).click();
  await page.getByLabel('Email address').fill(spare);
  await page.getByRole('button', { name: 'Send the invitation' }).click();
  await invitations.getByRole('button', { name: `Revoke ${spare}` }).click();
  const confirm = page.getByRole('dialog', { name: 'Revoke this invitation' });
  await confirm.getByRole('textbox').first().fill(spare);
  await confirm.getByRole('button', { name: 'Revoke the invitation' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: `Invitation to ${spare} revoked` }),
  ).toBeVisible();
  await expect(invitations.getByText(spare)).toHaveCount(0);
  await expectAccessible(page);
});
