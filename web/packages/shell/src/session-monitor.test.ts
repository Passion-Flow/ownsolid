import { ApiProblem, type SessionStatus } from '@ownsolid/api';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createSessionMonitor, type SessionWarning } from './session-monitor.ts';

const status = (idle: number, absolute = 10_000): SessionStatus => ({
  idle_timeout_seconds: 900,
  idle_remaining_seconds: idle,
  absolute_remaining_seconds: absolute,
  warning_seconds: 120,
});

function setup(first: SessionStatus) {
  let answer = first;
  const warnings: (SessionWarning | null)[] = [];
  const read = vi.fn((active: boolean) => {
    if (active) answer = { ...answer, idle_remaining_seconds: answer.idle_timeout_seconds };
    return Promise.resolve(answer);
  });
  const monitor = createSessionMonitor({
    read,
    pollMilliseconds: 5_000,
    onWarning: (warning) => warnings.push(warning),
  });
  return {
    monitor,
    read,
    warnings,
    answer: (next: SessionStatus) => {
      answer = next;
    },
    latest: () => warnings.at(-1) ?? null,
  };
}

const seconds = (value: number) => vi.advanceTimersByTimeAsync(value * 1_000);

describe('session monitor', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('leaves a quiet session alone until the warning is due', async () => {
    const { monitor, read, warnings } = setup(status(900));
    monitor.start();
    await seconds(0);
    expect(read).toHaveBeenCalledTimes(1);
    expect(read).toHaveBeenLastCalledWith(false);
    await seconds(779);
    expect(read).toHaveBeenCalledTimes(1);
    expect(warnings).toEqual([]);
    monitor.stop();
  });

  it('warns when the idle time runs into the window, counting to the server end', async () => {
    const { monitor, answer, latest } = setup(status(900));
    monitor.start();
    await seconds(0);
    answer(status(120));
    const expected = Date.now() + 780_000 + 120_000;
    await seconds(780);
    expect(latest()).toEqual({ reason: 'idle', deadline: expected });
    monitor.stop();
  });

  it('never extends the session on its own', async () => {
    const { monitor, read } = setup(status(100));
    monitor.start();
    await seconds(60);
    expect(read.mock.calls.every(([active]) => !active)).toBe(true);
    monitor.stop();
  });

  it('extends the session while the person is active, without a warning', async () => {
    const { monitor, read, warnings } = setup(status(900));
    monitor.start();
    await seconds(1);
    monitor.noteActivity();
    await seconds(120);
    expect(read).toHaveBeenLastCalledWith(true);
    expect(warnings).toEqual([]);
    monitor.stop();
  });

  it('asks once per window however busy the person is', async () => {
    const { monitor, read } = setup(status(900));
    monitor.start();
    await seconds(1);
    for (let elapsed = 0; elapsed < 240; elapsed += 1) {
      monitor.noteActivity();
      await seconds(1);
    }
    expect(read).toHaveBeenCalledTimes(3);
    monitor.stop();
  });

  it('does not let activity dismiss a warning that is showing', async () => {
    const { monitor, read, latest } = setup(status(100));
    monitor.start();
    await seconds(0);
    expect(latest()?.reason).toBe('idle');
    monitor.noteActivity();
    await seconds(10);
    expect(latest()?.reason).toBe('idle');
    expect(read).not.toHaveBeenCalledWith(true);
    monitor.stop();
  });

  it('closes the warning when another tab kept the session going', async () => {
    const { monitor, answer, latest } = setup(status(100));
    monitor.start();
    await seconds(0);
    answer(status(900));
    await seconds(5);
    expect(latest()).toBeNull();
    monitor.stop();
  });

  it('keeps a steady deadline across reads', async () => {
    const { monitor, answer, warnings } = setup(status(100));
    monitor.start();
    await seconds(0);
    answer(status(95));
    await seconds(5);
    expect(warnings).toHaveLength(1);
    monitor.stop();
  });

  it('stays signed in on request', async () => {
    const { monitor, read, latest } = setup(status(100));
    monitor.start();
    await seconds(0);
    await monitor.stay();
    expect(read).toHaveBeenLastCalledWith(true);
    expect(latest()).toBeNull();
    monitor.stop();
  });

  it('keeps the warning when staying fails, and lets the caller show why', async () => {
    const { monitor, read, latest } = setup(status(100));
    monitor.start();
    await seconds(0);
    read.mockRejectedValueOnce(new ApiProblem('NETWORK_UNAVAILABLE'));
    await expect(monitor.stay()).rejects.toThrow();
    expect(latest()?.reason).toBe('idle');
    monitor.stop();
  });

  it('keeps trying when the server cannot be reached', async () => {
    const { monitor, read, latest } = setup(status(100));
    monitor.start();
    await seconds(0);
    read.mockRejectedValueOnce(new ApiProblem('NETWORK_UNAVAILABLE'));
    const before = read.mock.calls.length;
    await seconds(10);
    expect(read.mock.calls.length).toBe(before + 2);
    expect(latest()?.reason).toBe('idle');
    monitor.stop();
  });

  it('warns about the time limit, which staying cannot extend', async () => {
    const { monitor, read, warnings, latest } = setup(status(900, 100));
    monitor.start();
    await seconds(0);
    expect(latest()?.reason).toBe('limit');
    monitor.dismissLimit();
    expect(latest()).toBeNull();
    const shown = warnings.length;
    await seconds(20);
    expect(warnings).toHaveLength(shown);
    expect(read.mock.calls.length).toBeGreaterThan(1);
    monitor.stop();
  });

  it('warns about inactivity first when that comes first', async () => {
    const { monitor, latest } = setup(status(50, 100));
    monitor.start();
    await seconds(0);
    expect(latest()?.reason).toBe('idle');
    monitor.stop();
  });

  it('stops reading once stopped', async () => {
    const { monitor, read } = setup(status(100));
    monitor.start();
    await seconds(0);
    monitor.stop();
    const before = read.mock.calls.length;
    await seconds(60);
    expect(read).toHaveBeenCalledTimes(before);
  });
});
