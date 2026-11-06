// The frame around every signed-in page: sidebar, top bar and the layers they open.
import { isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Brand,
  Button,
  DensityToggle,
  Kbd,
  ModalSurface,
  ProblemNotice,
  ThemeToggle,
  cn,
} from '@ownsolid/ui';
import { Link, useRouter, useRouterState } from '@tanstack/react-router';
import { ChevronRight, Menu as MenuIcon, Search, UserRound } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { AccountPreferences } from './account-preferences.tsx';
import { CommandMenu } from './command-menu.tsx';
import { LicenseStatus } from './license-status.tsx';
import { findNavItem, type NavGroup, type NavItem } from './navigation.ts';
import { NotificationBell } from './notifications.tsx';
import { useDocumentTitle } from './page-focus.ts';
import { accountPath, signInPath } from './redirect.ts';
import { SessionWarningDialog } from './session-warning.tsx';
import { useSignOut } from './sign-out.ts';
import { TaskTray, type TaskTrayProps } from './task-tray.tsx';
import { UserMenu } from './user-menu.tsx';
import { WorkspaceSwitcher } from './workspace-switcher.tsx';

const itemClasses =
  'flex h-[34px] items-center gap-2.5 rounded-control px-2.5 font-medium text-text-2 no-underline';

function Item({ item, onNavigate }: { item: NavItem; onNavigate?: (() => void) | undefined }) {
  const { t } = useI18n();
  if (!item.to) {
    return (
      <span
        aria-disabled
        title={t('nav.notAvailable')}
        className={cn(itemClasses, 'cursor-default opacity-50')}
      >
        <item.icon aria-hidden />
        <span className="truncate">{t(item.label)}</span>
      </span>
    );
  }
  return (
    <Link
      to={item.to}
      onClick={onNavigate}
      activeOptions={{ exact: item.to === '/' }}
      className={cn(
        itemClasses,
        'transition-colors duration-150 hover:bg-surface-2 hover:text-text',
        'data-[status=active]:bg-accent-soft data-[status=active]:font-semibold data-[status=active]:text-accent-ink',
      )}
    >
      <item.icon aria-hidden />
      <span className="truncate">{t(item.label)}</span>
    </Link>
  );
}

function Navigation({
  groups,
  onNavigate,
}: {
  groups: readonly NavGroup[];
  onNavigate?: (() => void) | undefined;
}) {
  const { t } = useI18n();
  return (
    <nav aria-label={t('nav.label')} className="px-2.5 pt-1 pb-5">
      {groups.map((group) => (
        <div key={group.heading ?? 'start'} className="mt-3.5">
          {group.heading ? (
            <div className="px-2.5 pb-1 text-[11.5px] font-semibold text-text-3">
              {t(group.heading)}
            </div>
          ) : null}
          {group.items.map((item) => (
            <Item key={item.label} item={item} onNavigate={onNavigate} />
          ))}
        </div>
      ))}
    </nav>
  );
}

function SidebarContent({
  groups,
  licensePath,
  onNavigate,
}: {
  groups: readonly NavGroup[];
  licensePath: string | undefined;
  onNavigate?: (() => void) | undefined;
}) {
  return (
    <>
      <Brand className="px-[18px] pt-[18px] pb-2.5" />
      <Navigation groups={groups} onNavigate={onNavigate} />
      {licensePath ? (
        <div className="mt-auto border-t border-border p-3.5">
          <LicenseStatus to={licensePath} onNavigate={onNavigate} />
        </div>
      ) : null}
    </>
  );
}

const macLike = /Mac|iPhone|iPad/.test(navigator.userAgent);

// The account page is reached from the user menu and the command menu, not the sidebar.
const accountPage: NavGroup = {
  items: [{ label: 'account.title', icon: UserRound, to: accountPath }],
};

/** Props for {@link AppFrame}. */
export interface AppFrameProps {
  navigation: readonly NavGroup[];
  /** Shows the license condition at the foot of the sidebar, as a link to this page. */
  licensePath?: string | undefined;
  /** Shows the tasks the person started; the portal has none. */
  tasks?: TaskTrayProps | undefined;
  children: ReactNode;
}

/** The signed-in layout of the console and the portal. */
export function AppFrame({ navigation, licensePath, tasks, children }: AppFrameProps) {
  const { t } = useI18n();
  const router = useRouter();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const [commandOpen, setCommandOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const signOut = useSignOut(() => {
    void router.navigate({ to: signInPath, replace: true });
  });
  const pages = useMemo(() => [...navigation, accountPage], [navigation]);
  const current = findNavItem(pages, pathname);
  const pageTitle = current ? t(current.item.label) : undefined;

  useDocumentTitle(pageTitle);

  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setCommandOpen((open) => !open);
      }
    };
    document.addEventListener('keydown', shortcut);
    return () => {
      document.removeEventListener('keydown', shortcut);
    };
  }, []);

  const startSignOut = () => {
    signOut.mutate();
  };

  return (
    <div className="grid h-dvh grid-cols-1 lg:grid-cols-[248px_1fr]">
      <AccountPreferences />
      <SessionWarningDialog onSignOut={startSignOut} />
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:m-2 focus:rounded-control focus:bg-surface focus:px-3 focus:py-2"
      >
        {t('nav.skip')}
      </a>
      <aside className="hidden flex-col overflow-y-auto border-r border-border bg-surface lg:flex">
        <SidebarContent groups={navigation} licensePath={licensePath} />
      </aside>
      <div className="flex h-dvh min-w-0 flex-col">
        <header className="sticky top-0 z-[5] flex h-[58px] flex-none items-center gap-2.5 border-b border-border px-4 lg:px-6">
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            aria-label={t('nav.open')}
            onClick={() => {
              setSidebarOpen(true);
            }}
          >
            <MenuIcon aria-hidden />
          </Button>
          {current ? (
            <div className="hidden min-w-0 items-center gap-1.5 text-[13px] text-text-3 md:flex">
              {current.group.heading ? (
                <>
                  <span className="truncate">{t(current.group.heading)}</span>
                  <ChevronRight aria-hidden className="size-3.5" />
                </>
              ) : null}
              <b className="truncate font-semibold text-text">{pageTitle}</b>
            </div>
          ) : null}
          <button
            type="button"
            aria-keyshortcuts={macLike ? 'Meta+K' : 'Control+K'}
            onClick={() => {
              setCommandOpen(true);
            }}
            className="ml-auto flex h-9 min-w-9 cursor-pointer items-center gap-2 rounded-field border border-border bg-surface px-2.5 text-text-3 xl:w-[320px] xl:pr-2.5 xl:pl-3"
          >
            <Search aria-hidden />
            {/* The prompt names the button; narrow screens show only the icon. */}
            <span className="sr-only flex-1 text-left text-[13px] xl:not-sr-only">
              {t('command.prompt')}
            </span>
            <span aria-hidden className="hidden xl:inline">
              <Kbd>{macLike ? '⌘K' : 'Ctrl K'}</Kbd>
            </span>
          </button>
          <WorkspaceSwitcher />
          <span className="hidden sm:contents">
            <DensityToggle />
            <ThemeToggle />
          </span>
          {tasks ? <TaskTray {...tasks} /> : null}
          <NotificationBell />
          <UserMenu onSignOut={startSignOut} />
        </header>
        <main id="main" tabIndex={-1} className="flex-1 overflow-y-auto outline-none">
          <div className="mx-auto max-w-[1320px] px-7 pt-7 pb-16">
            {signOut.error && isProblem(signOut.error) ? (
              <ProblemNotice className="mb-5" problem={signOut.error} onRetry={startSignOut} />
            ) : null}
            {children}
          </div>
        </main>
      </div>
      <ModalSurface
        open={sidebarOpen}
        onClose={() => {
          setSidebarOpen(false);
        }}
        label={t('nav.label')}
        className="my-0 mr-auto ml-0 h-dvh max-h-none w-[280px] max-w-[85vw] flex-col overflow-y-auto border-y-0 border-l-0 open:flex"
      >
        <SidebarContent
          groups={navigation}
          licensePath={licensePath}
          onNavigate={() => {
            setSidebarOpen(false);
          }}
        />
      </ModalSurface>
      <CommandMenu
        open={commandOpen}
        onClose={() => {
          setCommandOpen(false);
        }}
        navigation={pages}
        onSignOut={startSignOut}
      />
    </div>
  );
}
