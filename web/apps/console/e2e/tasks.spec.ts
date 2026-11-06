// Long tasks: an administrator starts work on a node, watches it, leaves, and is told how it ended.
import {
  failedTask,
  finishedTask,
  logLine,
  mockControlPlane,
  runningTask,
  type ControlPlane,
  type ScriptedTask,
} from '@ownsolid/shell/testing';
import { expect, test, type Page } from '@playwright/test';

let control: ControlPlane;

// The shortest interval the runtime configuration allows, so a flow does not wait long.
const poll = 1_000;

test.beforeEach(async ({ page }) => {
  control = await mockControlPlane(page, 'admin');
  control.signedIn = true;
  control.pollMilliseconds = poll;
});

const copy = (task: ScriptedTask): ScriptedTask => ({ ...task, log: [...task.log] });

test.describe('the task list', () => {
  test('is reached from the navigation and says when there are none', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Tasks' }).click();
    await expect(page.getByRole('heading', { name: 'Tasks', level: 1 })).toBeFocused();
    await expect(page.getByText('No tasks yet')).toBeVisible();
  });

  test('filters by status, from a link too', async ({ page }) => {
    control.tasks = [failedTask, finishedTask];
    await page.goto('/tasks');
    await expect(page.getByRole('link', { name: 'Collect node facts' })).toHaveCount(2);
    await page
      .getByRole('group', { name: 'Show tasks' })
      .getByRole('button', { name: 'Failed' })
      .click();
    await expect(page.getByRole('link', { name: 'Collect node facts' })).toHaveCount(1);
    await expect(page).toHaveURL(/state=failed/);
    await page.reload();
    await expect(page.getByRole('link', { name: 'Collect node facts' })).toHaveCount(1);
    await page
      .getByRole('group', { name: 'Show tasks' })
      .getByRole('button', { name: 'Running' })
      .click();
    await expect(page.getByText('No tasks match')).toBeVisible();
    await page.getByRole('button', { name: 'Show all tasks' }).click();
    await expect(page.getByRole('link', { name: 'Collect node facts' })).toHaveCount(2);
  });

  test('offers a retry when it cannot load', async ({ page }) => {
    control.tasksProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/tasks');
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
    control.tasksProblem = null;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByText('No tasks yet')).toBeVisible();
  });
});

test.describe('starting a task', () => {
  test('opens the task it started', async ({ page }) => {
    await page.goto('/tasks');
    await page.getByRole('button', { name: 'Collect node facts' }).click();
    const dialog = page.getByRole('dialog', { name: 'Collect node facts' });
    await expect(dialog.getByLabel('Node')).toHaveValue(control.nodes[0]?.id ?? '');
    await dialog.getByRole('button', { name: 'Start' }).click();
    await expect(page.getByRole('heading', { name: 'Collect node facts', level: 1 })).toBeVisible();
    await expect(page.getByText('Waiting for the node to pick this task up.')).toBeVisible();
  });

  test('keeps one key across a retry, so it cannot start twice', async ({ page }) => {
    control.nextTaskStart = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    await page.goto('/tasks');
    await page.getByRole('button', { name: 'Collect node facts' }).click();
    const dialog = page.getByRole('dialog', { name: 'Collect node facts' });
    await dialog.getByRole('button', { name: 'Start' }).click();
    await dialog.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('heading', { name: 'Collect node facts', level: 1 })).toBeVisible();
    expect(control.taskStartKeys).toHaveLength(2);
    expect(control.taskStartKeys[0]).toBe(control.taskStartKeys[1]);
  });

  test('says so when no node can run it', async ({ page }) => {
    control.nodes = [];
    await page.goto('/tasks');
    await page.getByRole('button', { name: 'Collect node facts' }).click();
    const dialog = page.getByRole('dialog', { name: 'Collect node facts' });
    await expect(dialog.getByText('No active node has joined yet.')).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Start' })).toBeDisabled();
  });
});

test.describe('watching a task', () => {
  const steps = (page: Page) =>
    page.getByRole('list', { name: 'Steps of this task' }).getByRole('listitem');

  test('fills in its steps and log as they arrive', async ({ page }) => {
    const task = copy(runningTask);
    control.tasks = [task];
    await page.goto(`/tasks/${task.task.id}`);
    await expect(steps(page)).toHaveCount(2);
    await expect(steps(page).first()).toContainText('Read the operating system');
    await expect(steps(page).first()).toContainText('3s');
    await expect(steps(page).nth(1)).toHaveAttribute('aria-current', 'step');
    await expect(page.getByRole('log')).toContainText('Operating system: ubuntu 24.04');

    task.log.push(logLine(4, 'Memory: 33554432000 bytes'));
    await expect(page.getByRole('log')).toContainText('Memory: 33554432000 bytes');

    control.tasks = [copy(finishedTask)];
    await expect(page.getByText('Succeeded')).toBeVisible();
    await expect(steps(page).nth(1)).not.toHaveAttribute('aria-current', 'step');
    await expect(page.getByRole('button', { name: 'Pause' })).toHaveCount(0);
  });

  test('reads only what is new, in the background', async ({ page }) => {
    const task = copy(runningTask);
    control.tasks = [task];
    const reads: (string | null)[] = [];
    page.on('request', (request) => {
      const url = new URL(request.url());
      if (url.pathname.endsWith('/log')) reads.push(url.searchParams.get('after'));
    });
    await page.goto(`/tasks/${task.task.id}`);
    await expect(page.getByRole('log')).toContainText('Operating system');
    await expect.poll(() => reads.length).toBeGreaterThan(1);
    expect(reads[0]).toBeNull();
    expect(reads[1]).toBe('1.2');
    expect(control.taskPolls.every(Boolean)).toBe(true);
  });

  test('stops reading once it has finished', async ({ page }) => {
    control.tasks = [copy(finishedTask)];
    let reads = 0;
    page.on('request', (request) => {
      if (new URL(request.url()).pathname.includes('/admin/tasks/')) reads += 1;
    });
    await page.goto(`/tasks/${finishedTask.task.id}`);
    await expect(page.getByRole('log')).toContainText('Memory');
    const settled = reads;
    await page.waitForTimeout(poll * 2.5);
    expect(reads).toBe(settled);
  });

  test('keeps what it has and offers a retry when a read fails', async ({ page }) => {
    const task = copy(runningTask);
    control.tasks = [task];
    await page.goto(`/tasks/${task.task.id}`);
    await expect(page.getByRole('log')).toContainText('Operating system');
    control.tasksProblem = { status: 503, code: 'STORAGE_UNAVAILABLE' };
    const retry = page.getByRole('button', { name: 'Try again' });
    await expect(retry).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole('log')).toContainText('Operating system');
    control.tasksProblem = null;
    task.log.push(logLine(4, 'Back again'));
    await retry.click();
    await expect(page.getByRole('log')).toContainText('Back again');
    await expect(retry).toHaveCount(0);
  });

  test('shows a task from an agent that reports no steps', async ({ page }) => {
    control.tasks = [{ ...finishedTask, steps: [], log: [] }];
    await page.goto(`/tasks/${finishedTask.task.id}`);
    await expect(page.getByText('This task reported no steps.')).toBeVisible();
    await expect(page.getByRole('log')).toContainText('No log lines yet.');
  });

  test('marks where a new attempt began', async ({ page }) => {
    control.tasks = [
      {
        ...finishedTask,
        task: { ...finishedTask.task, attempt: 2 },
        log: [logLine(2, 'First try'), logLine(2, 'Second try', 2)],
      },
    ];
    await page.goto(`/tasks/${finishedTask.task.id}`);
    const log = page.getByRole('log');
    await expect(log).toContainText('Second try');
    await expect(log).toContainText('Attempt 2 started');
  });

  test('reads a long log in pages and stays at its end', async ({ page }) => {
    const log = Array.from({ length: 1_200 }, (_, index) =>
      logLine(index + 1, `Line ${String(index + 1)}`),
    );
    control.tasks = [{ ...finishedTask, log }];
    await page.goto(`/tasks/${finishedTask.task.id}`);
    const box = page.getByRole('log');
    await expect(box).toContainText('Line 1200');
    await expect(box.locator('div')).toHaveCount(1_200);
    await expect
      .poll(() =>
        box.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight),
      )
      .toBeLessThan(10);
  });

  test('leaves the reader where they are when they scroll up', async ({ page }) => {
    const log = Array.from({ length: 60 }, (_, index) =>
      logLine(index + 1, `Line ${String(index + 1)}`),
    );
    const task = { ...runningTask, log };
    control.tasks = [task];
    await page.goto(`/tasks/${task.task.id}`);
    const box = page.getByRole('log');
    await expect(box).toContainText('Line 60');
    await box.evaluate((element) => {
      element.scrollTop = 0;
    });
    task.log = [...log, logLine(61, 'Line 61')];
    await expect(box).toContainText('Line 61');
    expect(await box.evaluate((element) => element.scrollTop)).toBe(0);
  });

  test('holds new lines back while paused', async ({ page }) => {
    const task = copy(runningTask);
    control.tasks = [task];
    await page.goto(`/tasks/${task.task.id}`);
    await expect(page.getByRole('log')).toContainText('Operating system');
    await page.getByRole('button', { name: 'Pause' }).click();
    task.log.push(logLine(4, 'Held back'));
    const resume = page.getByRole('button', { name: 'Resume (1 new)' });
    await expect(resume).toBeVisible();
    await expect(page.getByRole('log')).not.toContainText('Held back');
    await resume.click();
    await expect(page.getByRole('log')).toContainText('Held back');
  });

  test('downloads the log', async ({ page }) => {
    control.tasks = [copy(finishedTask)];
    await page.goto(`/tasks/${finishedTask.task.id}`);
    await expect(page.getByRole('log')).toContainText('Memory');
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Download' }).click(),
    ]);
    expect(download.suggestedFilename()).toBe(`task-${finishedTask.task.id}.log`);
  });

  test('says why a task failed and runs it again', async ({ page }) => {
    control.tasks = [copy(failedTask)];
    await page.goto(`/tasks/${failedTask.task.id}`);
    await expect(page.getByText('This task failed')).toBeVisible();
    await expect(page.getByText('The node could not read its own facts.')).toBeVisible();
    await expect(page.getByText('DISCOVERY_FAILED')).toBeVisible();
    await page.getByRole('button', { name: 'Run again' }).click();
    await expect(page.getByText('Waiting for the node to pick this task up.')).toBeVisible();
    expect(control.taskStartKeys).toHaveLength(1);
  });

  test('says so when the task does not exist', async ({ page }) => {
    await page.goto(`/tasks/${runningTask.task.id}`);
    await expect(page.getByRole('link', { name: 'All tasks' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Try again' })).toHaveCount(0);
  });
});

test.describe('leaving a running task', () => {
  const tray = (page: Page) =>
    page.getByRole('button', { name: /of your tasks running/ });

  test('shows it from any page and leads back to it', async ({ page }) => {
    control.tasks = [copy(runningTask)];
    await page.goto('/settings');
    await tray(page).click();
    await page.getByRole('menuitem', { name: /Collect node facts/ }).click();
    await expect(page).toHaveURL(new RegExp(`/tasks/${runningTask.task.id}$`));
    await expect(page.getByRole('log')).toContainText('Operating system');
  });

  test('polls in the background, so it keeps no session alive', async ({ page }) => {
    control.tasks = [copy(runningTask)];
    await page.goto('/settings');
    await expect(tray(page)).toBeVisible();
    expect(control.taskPolls.length).toBeGreaterThan(0);
    expect(control.taskPolls.every(Boolean)).toBe(true);
  });

  test('tells them when it finishes, once, on whatever page they are on', async ({ page }) => {
    control.tasks = [copy(runningTask)];
    await page.goto('/settings');
    await expect(tray(page)).toBeVisible();
    control.tasks = [copy(finishedTask)];
    const told = page.getByRole('status').filter({ hasText: 'Collect node facts finished' });
    await expect(told).toBeVisible();
    await expect(tray(page)).toHaveCount(0);
    await told.getByRole('button', { name: 'Open' }).click();
    await expect(page).toHaveURL(new RegExp(`/tasks/${finishedTask.task.id}$`));
    await page.reload();
    await expect(page.getByRole('log')).toContainText('Memory');
    await expect(told).toHaveCount(0);
  });

  test('tells them about a finish that happened while the page was closed', async ({ page }) => {
    control.tasks = [copy(runningTask)];
    await page.goto('/settings');
    await expect(tray(page)).toBeVisible();
    await page.goto('about:blank');
    control.tasks = [{ ...failedTask, task: { ...failedTask.task, id: runningTask.task.id } }];
    await page.goto('/settings');
    await expect(
      page.getByRole('status').filter({ hasText: 'Collect node facts did not succeed' }),
    ).toBeVisible();
  });

  test('counts the tasks that finish together', async ({ page }) => {
    const second = { ...runningTask, task: { ...runningTask.task, id: failedTask.task.id } };
    control.tasks = [copy(runningTask), second];
    await page.goto('/settings');
    await expect(tray(page)).toBeVisible();
    control.tasks = [copy(finishedTask), copy(failedTask)];
    await expect(page.getByRole('status').filter({ hasText: '2 tasks finished' })).toBeVisible();
  });
});
