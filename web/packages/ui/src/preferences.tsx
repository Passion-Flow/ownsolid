// Theme and density. This browser remembers them so a page opens without a flash; the
// signed-in shell connects them to the account, which decides across browsers. The
// resolved values are written to <html data-theme data-density>, which the tokens in
// styles.css read.
import {
  createContext,
  use,
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { applyTheme, type ThemePreference } from './theme.ts';

/** How tightly lists and cards are spaced. */
export type Density = 'comfortable' | 'compact';

const themeKey = 'ownsolid.theme';
const densityKey = 'ownsolid.density';
const shortcutsKey = 'ownsolid.shortcuts';
const navigationKey = 'ownsolid.navigation';

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    // Storage can be disabled by browser policy; defaults still work.
    return null;
  }
}

function write(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // The choice still applies to this page when storage is unavailable.
  }
}

function storedTheme(): ThemePreference {
  const value = read(themeKey);
  return value === 'light' || value === 'dark' ? value : 'system';
}

function storedDensity(): Density {
  return read(densityKey) === 'compact' ? 'compact' : 'comfortable';
}

function storedShortcuts(): boolean {
  return read(shortcutsKey) !== 'off';
}

function storedNavigationCollapsed(): boolean {
  return read(navigationKey) === 'collapsed';
}

/** The choices a user can save. */
export interface Appearance {
  theme: ThemePreference;
  density: Density;
  /** Whether one-character keyboard shortcuts work. */
  singleKeyShortcuts: boolean;
  /** Whether the sidebar shows only its icons. */
  navigationCollapsed: boolean;
}

/** Receives what the user changes, to save it beyond this browser. */
export type AppearanceSaver = (change: Partial<Appearance>) => void;

/** What {@link usePreferences} returns. */
export interface Preferences extends Appearance {
  setTheme: (theme: ThemePreference) => void;
  setDensity: (density: Density) => void;
  setSingleKeyShortcuts: (on: boolean) => void;
  setNavigationCollapsed: (collapsed: boolean) => void;
  /** Applies values saved elsewhere. Unlike the setters, this saves nothing. */
  adopt: (saved: Partial<Appearance>) => void;
  /** Sends later changes to `saver` and returns a function that stops. */
  connect: (saver: AppearanceSaver) => () => void;
}

const PreferencesContext = createContext<Preferences | null>(null);

/** Applies the stored theme and density to the document and provides them to the tree. */
export function PreferencesProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState(storedTheme);
  const [density, setDensityState] = useState(storedDensity);
  const [singleKeyShortcuts, setShortcutsState] = useState(storedShortcuts);
  const [navigationCollapsed, setNavigationState] = useState(storedNavigationCollapsed);
  const saver = useRef<AppearanceSaver | null>(null);

  useLayoutEffect(() => applyTheme(theme), [theme]);
  useLayoutEffect(() => {
    document.documentElement.dataset.density = density;
  }, [density]);

  const applyChoice = useCallback(
    ({ theme, density, singleKeyShortcuts, navigationCollapsed }: Partial<Appearance>) => {
      if (theme) {
        write(themeKey, theme);
        setThemeState(theme);
      }
      if (density) {
        write(densityKey, density);
        setDensityState(density);
      }
      if (singleKeyShortcuts !== undefined) {
        write(shortcutsKey, singleKeyShortcuts ? 'on' : 'off');
        setShortcutsState(singleKeyShortcuts);
      }
      if (navigationCollapsed !== undefined) {
        write(navigationKey, navigationCollapsed ? 'collapsed' : 'open');
        setNavigationState(navigationCollapsed);
      }
    },
    [],
  );
  const setTheme = useCallback(
    (value: ThemePreference) => {
      applyChoice({ theme: value });
      saver.current?.({ theme: value });
    },
    [applyChoice],
  );
  const setDensity = useCallback(
    (value: Density) => {
      applyChoice({ density: value });
      saver.current?.({ density: value });
    },
    [applyChoice],
  );
  const setNavigationCollapsed = useCallback(
    (value: boolean) => {
      applyChoice({ navigationCollapsed: value });
      saver.current?.({ navigationCollapsed: value });
    },
    [applyChoice],
  );
  const setSingleKeyShortcuts = useCallback(
    (value: boolean) => {
      applyChoice({ singleKeyShortcuts: value });
      saver.current?.({ singleKeyShortcuts: value });
    },
    [applyChoice],
  );
  const connect = useCallback((next: AppearanceSaver) => {
    saver.current = next;
    return () => {
      saver.current = null;
    };
  }, []);

  const value = useMemo(
    () => ({
      theme,
      setTheme,
      density,
      setDensity,
      singleKeyShortcuts,
      setSingleKeyShortcuts,
      navigationCollapsed,
      setNavigationCollapsed,
      adopt: applyChoice,
      connect,
    }),
    [
      theme,
      setTheme,
      density,
      setDensity,
      singleKeyShortcuts,
      setSingleKeyShortcuts,
      navigationCollapsed,
      setNavigationCollapsed,
      applyChoice,
      connect,
    ],
  );
  return <PreferencesContext value={value}>{children}</PreferencesContext>;
}

/** Returns the theme and density. Must be used inside {@link PreferencesProvider}. */
export function usePreferences(): Preferences {
  const value = use(PreferencesContext);
  if (!value) throw new Error('usePreferences must be used inside PreferencesProvider');
  return value;
}
