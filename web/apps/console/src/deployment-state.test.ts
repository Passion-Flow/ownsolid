import type { Deployment } from '@ownsolid/api';
import { describe, expect, it } from 'vitest';
import { canStart, canStop, deploymentStatus } from './deployment-state.ts';

const running = {
  desired_state: 'running',
  observed_state: 'running',
  observation: { ready: true },
  task_state: 'succeeded',
  task_error_code: null,
  resource_state: 'held',
  stale: false,
  removed_at: null,
} as unknown as Deployment;

const read = (change: Partial<Deployment>) => deploymentStatus({ ...running, ...change });

describe('deploymentStatus', () => {
  it('calls a deployment healthy only when it answers and reports', () => {
    expect(read({}).phase).toBe('healthy');
    expect(read({ observation: null }).phase).toBe('degraded');
    expect(read({ stale: true }).label).toBe('services.status.notReporting');
  });

  it('puts work in progress first, by what is being asked', () => {
    expect(read({ task_state: 'pending' }).label).toBe('services.status.starting');
    expect(read({ task_state: 'running', desired_state: 'stopped' }).label).toBe(
      'services.status.stopping',
    );
    expect(read({ task_state: 'running', desired_state: 'removed' }).label).toBe(
      'services.status.removing',
    );
    expect(read({ task_state: 'succeeded', observed_state: 'loading' }).phase).toBe('changing');
  });

  it('keeps the code that explains a failure', () => {
    const failed = read({
      observed_state: 'failed',
      task_state: 'failed',
      task_error_code: 'INFERENCE_GPU_OOM',
      observation: null,
    });
    expect(failed.phase).toBe('degraded');
    expect(failed.error).toBe('INFERENCE_GPU_OOM');
  });

  it('never calls a deployment stopped on a report it cannot trust', () => {
    const stopped = { desired_state: 'stopped', observed_state: 'stopped' } as const;
    expect(read(stopped).phase).toBe('stopped');
    expect(read({ ...stopped, stale: true }).phase).toBe('degraded');
  });

  it('holds a GPU needing recovery above anything it last reported', () => {
    expect(read({ resource_state: 'recovery_required' }).label).toBe('services.status.recovery');
  });

  it('calls a removed deployment removed, whatever it last reported', () => {
    expect(read({ removed_at: '2026-11-07T00:00:00Z' }).label).toBe('services.status.removed');
  });
});

describe('what a person can do', () => {
  it('starts only what is neither running nor changing nor stuck', () => {
    expect(canStart(running)).toBe(false);
    expect(canStart({ ...running, desired_state: 'stopped', observed_state: 'stopped' })).toBe(
      true,
    );
    expect(canStart({ ...running, task_state: 'pending' })).toBe(false);
    expect(canStart({ ...running, stale: true, resource_state: 'recovery_required' })).toBe(false);
  });

  it('stops what means to run, once nothing is changing it', () => {
    expect(canStop(running)).toBe(true);
    expect(canStop({ ...running, task_state: 'running' })).toBe(false);
    expect(canStop({ ...running, desired_state: 'stopped' })).toBe(false);
  });
});
