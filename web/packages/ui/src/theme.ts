// Theme selection. The resolved theme is written to <html data-theme>, which the
// tokens in styles.css read.

/** A theme preference: a fixed theme or the operating system's choice. */
export type ThemePreference = 'light' | 'dark' | 'system';

const query = '(prefers-color-scheme: dark)';

/** Resolves a preference to the theme to render. */
export function resolveTheme(preference: ThemePreference): 'light' | 'dark' {
  if (preference !== 'system') return preference;
  return window.matchMedia(query).matches ? 'dark' : 'light';
}

/**
 * Applies a preference to the document and, for `system`, follows later operating
 * system changes. Returns a function that stops following them.
 */
export function applyTheme(preference: ThemePreference): () => void {
  const root = document.documentElement;
  root.dataset.theme = resolveTheme(preference);
  if (preference !== 'system') return () => undefined;
  const media = window.matchMedia(query);
  const update = () => {
    root.dataset.theme = media.matches ? 'dark' : 'light';
  };
  media.addEventListener('change', update);
  return () => {
    media.removeEventListener('change', update);
  };
}
