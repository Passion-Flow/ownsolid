import { useI18n } from '@ownsolid/i18n';
import { Moon, Rows3, Sun } from 'lucide-react';
import { usePreferences } from '../preferences.tsx';
import { resolveTheme } from '../theme.ts';
import { Button } from './button.tsx';

/** Switches between the light and dark themes. */
export function ThemeToggle() {
  const { t } = useI18n();
  const { theme, setTheme } = usePreferences();
  const dark = resolveTheme(theme) === 'dark';
  const label = dark ? t('preferences.useLightTheme') : t('preferences.useDarkTheme');
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={label}
      title={label}
      onClick={() => {
        setTheme(dark ? 'light' : 'dark');
      }}
    >
      {dark ? <Sun aria-hidden /> : <Moon aria-hidden />}
    </Button>
  );
}

/** Switches between comfortable and compact spacing. */
export function DensityToggle() {
  const { t } = useI18n();
  const { density, setDensity } = usePreferences();
  const compact = density === 'compact';
  const label = compact ? t('preferences.useComfortable') : t('preferences.useCompact');
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={label}
      title={label}
      onClick={() => {
        setDensity(compact ? 'comfortable' : 'compact');
      }}
    >
      <Rows3 aria-hidden />
    </Button>
  );
}
