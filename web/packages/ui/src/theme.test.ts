import { afterEach, describe, expect, it, vi } from 'vitest';
import { applyTheme, resolveTheme } from './theme.ts';

function stubMedia(dark: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: dark,
    addEventListener: (_: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
  };
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => media),
  );
  return {
    listeners,
    set(value: boolean) {
      media.matches = value;
      listeners.forEach((listener) => {
        listener();
      });
    },
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.theme;
});

describe('resolveTheme', () => {
  it('keeps a fixed preference and asks the system otherwise', () => {
    stubMedia(true);
    expect(resolveTheme('light')).toBe('light');
    expect(resolveTheme('system')).toBe('dark');
  });
});

describe('applyTheme', () => {
  it('follows the system until stopped', () => {
    const media = stubMedia(false);
    const stop = applyTheme('system');
    expect(document.documentElement.dataset.theme).toBe('light');
    media.set(true);
    expect(document.documentElement.dataset.theme).toBe('dark');
    stop();
    expect(media.listeners.size).toBe(0);
  });

  it('does not follow the system for a fixed theme', () => {
    const media = stubMedia(false);
    applyTheme('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(media.listeners.size).toBe(0);
  });
});
