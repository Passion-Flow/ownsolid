// The top bar's help button: the article about the current page, or the help page.
import { articleForPath } from '@ownsolid/help';
import { useI18n } from '@ownsolid/i18n';
import { Link, useRouterState } from '@tanstack/react-router';
import { CircleHelp } from 'lucide-react';
import { useHelp } from './entrance.ts';

/** Opens the help for the page on screen. */
export function HelpLink() {
  const { t } = useI18n();
  const { entrance } = useHelp();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const slug = articleForPath(entrance, pathname);
  const label = slug ? t('help.forPage') : t('help.title');
  const classes =
    'grid size-9 flex-none place-items-center rounded-control text-text-2 transition-colors duration-150 hover:bg-surface-2 hover:text-text';
  return slug ? (
    <Link to="/help/$slug" params={{ slug }} aria-label={label} title={label} className={classes}>
      <CircleHelp aria-hidden className="size-[18px]" />
    </Link>
  ) : (
    <Link to="/help" aria-label={label} title={label} className={classes}>
      <CircleHelp aria-hidden className="size-[18px]" />
    </Link>
  );
}
