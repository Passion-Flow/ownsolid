import { ApiProblem, type Preference, type PreferenceChange } from '@ownsolid/api';
import { describe, expect, it, vi } from 'vitest';
import { createPreferenceSync, type PreferenceSyncOptions } from './preference-sync.ts';

const saved = (revision: number, change: PreferenceChange = {}): Preference => ({
  theme: 'system',
  density: 'comfortable',
  locale: 'auto',
  revision,
  updated_at: '2026-11-05T00:00:00Z',
  ...change,
});

const problem = (code: string, status: number) => new ApiProblem(code, { status });

function setup(overrides: Partial<PreferenceSyncOptions> = {}) {
  let current: Preference | undefined = saved(1);
  const options = {
    known: () => current,
    load: vi.fn(() => Promise.resolve(saved(5))),
    save: vi.fn((change: PreferenceChange, revision: number) =>
      Promise.resolve(saved(revision + 1, change)),
    ),
    onSaved: vi.fn((value: Preference) => {
      current = value;
    }),
    onFailed: vi.fn(),
    ...overrides,
  };
  return { options, sync: createPreferenceSync(options) };
}

describe('createPreferenceSync', () => {
  it('saves on top of the known revision', async () => {
    const { options, sync } = setup();
    sync.save({ theme: 'dark' });
    expect(sync.busy()).toBe(true);
    await vi.waitFor(() => {
      expect(sync.busy()).toBe(false);
    });
    expect(options.save).toHaveBeenCalledExactlyOnceWith({ theme: 'dark' }, 1);
    expect(options.onSaved).toHaveBeenCalledOnce();
  });

  it('reads the revision first when none is known', async () => {
    const { options, sync } = setup({ known: () => undefined });
    sync.save({ density: 'compact' });
    await vi.waitFor(() => {
      expect(options.onSaved).toHaveBeenCalledOnce();
    });
    expect(options.save).toHaveBeenCalledExactlyOnceWith({ density: 'compact' }, 5);
  });

  it('merges changes made during a save into one later request', async () => {
    const release: (() => void)[] = [];
    const first = new Promise<void>((resolve) => release.push(resolve));
    const save = vi.fn(async (change: PreferenceChange, revision: number) => {
      if (revision === 1) await first;
      return saved(revision + 1, change);
    });
    const { options, sync } = setup({ save });
    sync.save({ theme: 'dark' });
    await vi.waitFor(() => {
      expect(save).toHaveBeenCalledOnce();
    });
    sync.save({ theme: 'light' });
    sync.save({ density: 'compact' });
    release[0]?.();
    await vi.waitFor(() => {
      expect(sync.busy()).toBe(false);
    });
    expect(save).toHaveBeenCalledTimes(2);
    expect(save).toHaveBeenLastCalledWith({ theme: 'light', density: 'compact' }, 2);
    expect(options.onSaved).toHaveBeenCalledTimes(2);
  });

  it('applies the change on the newer revision when another browser saved first', async () => {
    const save = vi
      .fn<PreferenceSyncOptions['save']>()
      .mockRejectedValueOnce(problem('PREFERENCE_REVISION_CONFLICT', 409))
      .mockResolvedValueOnce(saved(6, { theme: 'dark' }));
    const { options, sync } = setup({ save });
    sync.save({ theme: 'dark' });
    await vi.waitFor(() => {
      expect(options.onSaved).toHaveBeenCalledOnce();
    });
    expect(save).toHaveBeenLastCalledWith({ theme: 'dark' }, 5);
    expect(options.onFailed).not.toHaveBeenCalled();
  });

  it('reports a second conflict instead of looping', async () => {
    const save = vi
      .fn<PreferenceSyncOptions['save']>()
      .mockRejectedValue(problem('PREFERENCE_REVISION_CONFLICT', 409));
    const { options, sync } = setup({ save });
    sync.save({ theme: 'dark' });
    await vi.waitFor(() => {
      expect(options.onFailed).toHaveBeenCalledOnce();
    });
    expect(save).toHaveBeenCalledTimes(2);
  });

  it('keeps a failed change unsaved until a retry succeeds', async () => {
    const failure = problem('STORAGE_UNAVAILABLE', 503);
    const save = vi
      .fn<PreferenceSyncOptions['save']>()
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(saved(2, { theme: 'dark' }));
    const onFailed = vi.fn<PreferenceSyncOptions['onFailed']>();
    const { options, sync } = setup({ save, onFailed });
    sync.save({ theme: 'dark' });
    await vi.waitFor(() => {
      expect(onFailed).toHaveBeenCalledOnce();
    });
    expect(onFailed.mock.calls[0]?.[0]).toBe(failure);
    expect(sync.busy()).toBe(true);
    onFailed.mock.calls[0]?.[1]();
    await vi.waitFor(() => {
      expect(sync.busy()).toBe(false);
    });
    expect(save).toHaveBeenLastCalledWith({ theme: 'dark' }, 1);
    expect(options.onSaved).toHaveBeenCalledOnce();
  });

  it('sends a failed change along with the next one', async () => {
    const save = vi
      .fn<PreferenceSyncOptions['save']>()
      .mockRejectedValueOnce(problem('STORAGE_UNAVAILABLE', 503))
      .mockResolvedValueOnce(saved(2));
    const { options, sync } = setup({ save });
    sync.save({ theme: 'dark' });
    await vi.waitFor(() => {
      expect(options.onFailed).toHaveBeenCalledOnce();
    });
    sync.save({ density: 'compact' });
    await vi.waitFor(() => {
      expect(sync.busy()).toBe(false);
    });
    expect(save).toHaveBeenLastCalledWith({ theme: 'dark', density: 'compact' }, 1);
  });
});
