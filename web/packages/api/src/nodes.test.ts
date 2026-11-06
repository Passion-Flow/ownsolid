import { describe, expect, it } from 'vitest';
import { slowestHeartbeat } from './nodes.ts';

// Control accepts a pacing when 3 * heartbeat * (100 + jitter) <= 200 * window
// (api/agent/v1/pacing.go, FitsOnlineWindow); the page must agree at every edge.
const fits = (window: number, heartbeat: number, jitter: number) =>
  3 * heartbeat * (100 + jitter) <= 200 * window;

describe('slowestHeartbeat', () => {
  it('matches the control plane at and beyond the edge', () => {
    for (const window of [5, 25, 27, 30, 90, 600]) {
      for (const jitter of [0, 1, 10, 33, 50]) {
        const slowest = slowestHeartbeat(window, jitter);
        expect(fits(window, slowest, jitter)).toBe(true);
        expect(fits(window, slowest + 1, jitter)).toBe(false);
      }
    }
  });

  it('allows 20 seconds without jitter in the default 30-second window', () => {
    expect(slowestHeartbeat(30, 0)).toBe(20);
  });
});
