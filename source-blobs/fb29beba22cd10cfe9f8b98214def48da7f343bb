import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider, usePreferences, type AppearanceSaver } from './preferences.tsx';

function Probe({ saver }: { saver?: AppearanceSaver }) {
  const { theme, setTheme, density, setDensity, adopt, connect } = usePreferences();
  useEffect(() => (saver ? connect(saver) : undefined), [connect, saver]);
  return (
    <>
      <button
        type="button"
        onClick={() => {
          adopt({ theme: 'dark', density: 'compact' });
        }}
      >
        adopt
      </button>
      <button
        type="button"
        onClick={() => {
          setTheme('dark');
        }}
      >
        {theme}
      </button>
      <button
        type="button"
        onClick={() => {
          setDensity('compact');
        }}
      >
        {density}
      </button>
    </>
  );
}

beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      matches: false,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    })),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('PreferencesProvider', () => {
  it('starts from the system theme and comfortable density', () => {
    render(
      <PreferencesProvider>
        <Probe />
      </PreferencesProvider>,
    );
    expect(screen.getByRole('button', { name: 'system' })).toBeInTheDocument();
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(document.documentElement.dataset.density).toBe('comfortable');
  });

  it('applies and remembers changes', async () => {
    render(
      <PreferencesProvider>
        <Probe />
      </PreferencesProvider>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'system' }));
    await userEvent.click(screen.getByRole('button', { name: 'comfortable' }));
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(document.documentElement.dataset.density).toBe('compact');
    expect(localStorage.getItem('ownsolid.theme')).toBe('dark');
    expect(localStorage.getItem('ownsolid.density')).toBe('compact');
  });

  it('sends what the user changes to a connected saver', async () => {
    const saver = vi.fn();
    render(
      <PreferencesProvider>
        <Probe saver={saver} />
      </PreferencesProvider>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'system' }));
    await userEvent.click(screen.getByRole('button', { name: 'comfortable' }));
    expect(saver.mock.calls).toEqual([[{ theme: 'dark' }], [{ density: 'compact' }]]);
  });

  it('applies and remembers saved values without saving them again', async () => {
    const saver = vi.fn();
    render(
      <PreferencesProvider>
        <Probe saver={saver} />
      </PreferencesProvider>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'adopt' }));
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(document.documentElement.dataset.density).toBe('compact');
    expect(localStorage.getItem('ownsolid.theme')).toBe('dark');
    expect(saver).not.toHaveBeenCalled();
  });

  it('starts from stored choices and ignores invalid ones', () => {
    localStorage.setItem('ownsolid.theme', 'dark');
    localStorage.setItem('ownsolid.density', 'huge');
    render(
      <PreferencesProvider>
        <Probe />
      </PreferencesProvider>,
    );
    expect(screen.getByRole('button', { name: 'dark' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'comfortable' })).toBeInTheDocument();
  });
});
