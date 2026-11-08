// What a gateway key's state means to a person, which tile counts it, and the lifetimes a new key
// can be given (docs/research/gateway-keys.md).
import {
  maximumRotationOverlapSeconds,
  type GatewayKey,
  type GatewayKeyPolicy,
} from '@ownsolid/api';
import type { StatusTone } from '@ownsolid/ui';

/** Where the page's queries live, so a write refreshes exactly what it changed. */
export const gatewayKeys = {
  all: ['gateway-keys'] as const,
  list: (workspace: string) => ['gateway-keys', 'list', workspace] as const,
  calls: (workspace: string, key: string) => ['gateway-keys', 'calls', workspace, key] as const,
  aliases: (workspace: string) => ['gateway-keys', 'aliases', workspace] as const,
};

/**
 * A key ending within this long is expiring soon: the longest overlap a rotation can give, so a
 * key rotated as soon as it shows here still has a replacement before it stops.
 */
export const expiringSoonMilliseconds = maximumRotationOverlapSeconds * 1000;

/** The tiles above the list. `overLimit` waits for per-key limits (GW-106). */
export const keyTiles = ['all', 'expiring', 'unused', 'overLimit'] as const;

export type KeyTile = (typeof keyTiles)[number];

/** The tiles a person can choose; the rest are shown as not ready. */
export const readyTiles: readonly KeyTile[] = ['all', 'expiring', 'unused'];

/** True when the key works now and stops within {@link expiringSoonMilliseconds}. */
export function expiringSoon(key: GatewayKey, now: number): boolean {
  return key.state === 'active' && Date.parse(key.expires_at) - now <= expiringSoonMilliseconds;
}

/** True when the key works and has never been used. */
export function unused(key: GatewayKey): boolean {
  return key.state === 'active' && key.last_used_at === null;
}

/** Whether a key belongs under a tile. */
export function inTile(key: GatewayKey, tile: KeyTile, now: number): boolean {
  if (tile === 'expiring') return expiringSoon(key, now);
  if (tile === 'unused') return unused(key);
  return tile === 'all';
}

/** The pill tone of a key's state. */
export function keyTone(key: GatewayKey): StatusTone {
  if (key.state === 'active') return key.replaced_by === null ? 'good' : 'warning';
  if (key.state === 'revoked' || key.state === 'expired') return 'neutral';
  return 'critical';
}

/** The message key of a key's state; an active key that was replaced says it is being replaced. */
export function keyStateLabel(
  key: GatewayKey,
): 'gateway.state.replaced' | `gateway.state.${GatewayKey['state']}` {
  return key.state === 'active' && key.replaced_by !== null
    ? 'gateway.state.replaced'
    : `gateway.state.${key.state}`;
}

/** True when a key can still be rotated: it works and has not been replaced. */
export function canRotate(key: GatewayKey): boolean {
  return key.state === 'active' && key.replaced_by === null;
}

/** True when revoking would stop something: the key has not stopped yet. */
export function canRevoke(key: GatewayKey): boolean {
  return key.state !== 'revoked' && key.state !== 'expired';
}

const day = 24 * 60 * 60;

/** Lifetimes offered when issuing a key, in seconds. */
const lifetimePresets = [day, 7 * day, 30 * day, 90 * day];

/**
 * The lifetimes a new key can be given: the installation's default and the presets it allows,
 * shortest first.
 */
export function lifetimes(policy: GatewayKeyPolicy): number[] {
  const allowed = lifetimePresets.filter((seconds) => seconds <= policy.maximumTtlSeconds);
  return [...new Set([...allowed, policy.defaultTtlSeconds])].sort((a, b) => a - b);
}

/** Overlaps a rotation can give, in seconds: none, an hour, a day and the longest allowed. */
export const overlaps = [0, 3_600, 86_400, 604_800] as const;

export type Overlap = (typeof overlaps)[number];
