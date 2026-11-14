import type { AsyncRouteComponent } from '@tanstack/react-router';
import { describe, expect, it, vi } from 'vitest';
import { preloading } from './lazy-route.ts';

function part(preload?: () => Promise<void>): AsyncRouteComponent<never> {
  return Object.assign(() => null, preload ? { preload } : {});
}

describe('preloading', () => {
  it('preloads every part the route component renders', async () => {
    const first = vi.fn(() => Promise.resolve());
    const second = vi.fn(() => Promise.resolve());
    const Route = preloading(() => null, part(first), part(second));
    await Route.preload?.();
    expect(first).toHaveBeenCalledOnce();
    expect(second).toHaveBeenCalledOnce();
  });

  it('skips a part that has already loaded', async () => {
    const pending = vi.fn(() => Promise.resolve());
    const Route = preloading(() => null, part(), part(pending));
    await expect(Route.preload?.()).resolves.toBeUndefined();
    expect(pending).toHaveBeenCalledOnce();
  });

  it('passes on a failed load so the router can show its error', async () => {
    const Route = preloading(
      () => null,
      part(() => Promise.reject(new Error('offline'))),
    );
    await expect(Route.preload?.()).rejects.toThrow('offline');
  });
});
