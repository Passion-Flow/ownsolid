import { maximumRotationOverlapSeconds, type GatewayKey } from '@ownsolid/api';
import { describe, expect, it } from 'vitest';
import {
  canRevoke,
  canRotate,
  expiringSoon,
  inTile,
  keyStateLabel,
  keyTone,
  lifetimes,
  overlaps,
  unused,
} from './key-state.ts';

const now = Date.parse('2026-11-08T00:00:00Z');
const day = 24 * 60 * 60 * 1000;

const active = {
  state: 'active',
  expires_at: new Date(now + 30 * day).toISOString(),
  last_used_at: '2026-11-07T00:00:00Z',
  replaced_by: null,
} as unknown as GatewayKey;

const key = (change: Partial<GatewayKey>) => ({ ...active, ...change });

describe('tiles', () => {
  it('counts a working key ending within a week as expiring soon', () => {
    expect(expiringSoon(key({ expires_at: new Date(now + 7 * day).toISOString() }), now)).toBe(
      true,
    );
    expect(expiringSoon(key({ expires_at: new Date(now + 7 * day + 1).toISOString() }), now)).toBe(
      false,
    );
    expect(
      expiringSoon(key({ state: 'revoked', expires_at: new Date(now + day).toISOString() }), now),
    ).toBe(false);
  });

  it('counts only working keys never used as unused', () => {
    expect(unused(key({ last_used_at: null }))).toBe(true);
    expect(unused(active)).toBe(false);
    expect(unused(key({ last_used_at: null, state: 'expired' }))).toBe(false);
  });

  it('puts every key under all, and none under a tile that is not ready', () => {
    expect(inTile(active, 'all', now)).toBe(true);
    expect(inTile(active, 'overLimit', now)).toBe(false);
  });
});

describe('state', () => {
  it('says a replaced key that still works is being replaced, and cannot rotate again', () => {
    const replaced = key({ replaced_by: '6f1c7a1e-0000-4000-8000-000000000001' });
    expect(keyStateLabel(replaced)).toBe('gateway.state.replaced');
    expect(keyTone(replaced)).toBe('warning');
    expect(canRotate(replaced)).toBe(false);
    expect(canRevoke(replaced)).toBe(true);
  });

  it('offers nothing for a key that has stopped', () => {
    for (const state of ['revoked', 'expired'] as const) {
      expect(canRotate(key({ state }))).toBe(false);
      expect(canRevoke(key({ state }))).toBe(false);
      expect(keyTone(key({ state }))).toBe('neutral');
    }
    expect(keyTone(key({ state: 'invalidated' }))).toBe('critical');
    expect(canRevoke(key({ state: 'invalidated' }))).toBe(true);
  });
});

describe('lifetimes', () => {
  it('offers only what the installation allows, with its default', () => {
    expect(lifetimes({ defaultTtlSeconds: 86_400, maximumTtlSeconds: 7_776_000 })).toEqual([
      86_400, 604_800, 2_592_000, 7_776_000,
    ]);
    expect(lifetimes({ defaultTtlSeconds: 3_600, maximumTtlSeconds: 604_800 })).toEqual([
      3_600, 86_400, 604_800,
    ]);
  });
});

describe('overlaps', () => {
  it('ends at the longest overlap the server accepts', () => {
    expect(overlaps.at(-1)).toBe(maximumRotationOverlapSeconds);
  });
});
