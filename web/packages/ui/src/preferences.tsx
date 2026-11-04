// Theme and density choices, remembered in this browser. The resolved values are
// written to <html data-theme data-density>, which the tokens in styles.css read.
import {
  createContext,
  use,
  useCallback,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { applyTheme, type ThemePreference } from './theme.ts';

/** How tightly lists and cards are spaced. */
export type Density = 'comfortable' | 'compact';

const themeKey = 'ownsolid.theme';
const densityKey = 'ownsolid.density';

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

/** What {@link usePreferences} returns. */
export interface Preferences {
  theme: ThemePreference;
  setTheme: (theme: ThemePreference) => void;
  density: Density;
  setDensity: (density: Density) => void;
}

const PreferencesContext = createContext<Preferences | null>(null);

/** Applies the stored theme and density to the document and provides them to the tree. */
export function PreferencesProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState(storedTheme);
  const [density, setDensityState] = useState(storedDensity);

  useLayoutEffect(() => applyTheme(theme), [theme]);
  useLayoutEffect(() => {
    document.documentElement.dataset.density = density;
  }, [density]);

  const setTheme = useCallback((value: ThemePreference) => {
    write(themeKey, value);
    setThemeState(value);
  }, []);
  const setDensity = useCallback((value: Density) => {
    write(densityKey, value);
    setDensityState(value);
  }, []);

  const value = useMemo(
    () => ({ theme, setTheme, density, setDensity }),
    [theme, setTheme, density, setDensity],
  );
  return <PreferencesContext value={value}>{children}</PreferencesContext>;
}

/** Returns the theme and density. Must be used inside {@link PreferencesProvider}. */
export function usePreferences(): Preferences {
  const value = use(PreferencesContext);
  if (!value) throw new Error('usePreferences must be used inside PreferencesProvider');
  return value;
}
