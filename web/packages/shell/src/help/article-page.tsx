// `/help/<slug>`: one article, its sections and the pages it explains (plan S-107).
import { article, loadArticle } from '@ownsolid/help';
import { useI18n } from '@ownsolid/i18n';
import { Button, Card, EmptyState, Notice, Skeleton } from '@ownsolid/ui';
import { useQuery } from '@tanstack/react-query';
import { Link, useRouterState } from '@tanstack/react-router';
import { ArrowLeft, FileQuestion } from 'lucide-react';
import { useHelp } from './entrance.ts';
import { ArticleContent } from './markdown.tsx';

const linkClasses = 'font-semibold text-accent-ink hover:underline';

function BackLink() {
  const { t } = useI18n();
  return (
    <Link
      to="/help"
      className="mb-4 inline-flex items-center gap-1.5 text-[13px] font-semibold text-accent-ink hover:underline"
    >
      <ArrowLeft aria-hidden className="size-4" />
      {t('help.back')}
    </Link>
  );
}

/** One help article. */
export function ArticlePage() {
  const { t } = useI18n();
  // The shell's routes are typed by each application, so the article is read from the address.
  const slug = useRouterState({
    select: (state) => decodeURIComponent(state.location.pathname.replace(/^\/help\//, '')),
  });
  const { entrance, locale } = useHelp();
  const entry = article(entrance, locale, slug);
  // The article is a file of this installation; it fails to load only when the connection drops
  // or an upgrade replaced it, and both pass on a retry.
  const body = useQuery({
    queryKey: ['help', entrance, locale, slug],
    queryFn: () => loadArticle(entrance, locale, slug),
    enabled: entry !== undefined,
    staleTime: 'static',
  });

  if (!entry) {
    return (
      <>
        <BackLink />
        <Card>
          <EmptyState
            icon={FileQuestion}
            title={t('help.missing.title')}
            action={
              <Link to="/help" className={linkClasses}>
                {t('help.missing.action')}
              </Link>
            }
          >
            {t('help.missing.body')}
          </EmptyState>
        </Card>
      </>
    );
  }

  return (
    <>
      <BackLink />
      <h1 className="m-0 mb-1 max-w-[720px] text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {entry.title}
      </h1>
      <p className="m-0 mb-4 max-w-[720px] text-text-2">{entry.summary}</p>
      {entry.sections.length > 1 ? (
        <nav aria-label={t('help.contents')} className="mb-2">
          <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[13px]">
            {entry.sections.map((section) => (
              <li key={section.anchor}>
                <a href={`#${section.anchor}`} className={linkClasses}>
                  {section.title}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      ) : null}
      {body.data ? (
        <ArticleContent tree={body.data} />
      ) : body.isError ? (
        <Notice
          tone="critical"
          className="max-w-[720px]"
          title={t('help.loadFailed.title')}
          actions={
            <Button
              onClick={() => {
                void body.refetch();
              }}
            >
              {t('action.retry')}
            </Button>
          }
        >
          {t('help.loadFailed.body')}
        </Notice>
      ) : (
        <div aria-busy className="flex max-w-[720px] flex-col gap-3">
          <Skeleton />
          <Skeleton />
          <Skeleton className="h-4 w-2/3" />
        </div>
      )}
    </>
  );
}
