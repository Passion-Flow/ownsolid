// How to call with a key: the endpoint, a sample in each language, and what a private certificate
// authority needs. It never holds the secret, which was shown once when the key was issued
// (docs/research/portal-api-keys.md).
import { downloadGatewayAliasCA, isProblem, type GatewayKey } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  CodeBlock,
  CopyText,
  Detail,
  DetailList,
  Notice,
  ProblemNotice,
  saveFile,
  Skeleton,
  Tabs,
} from '@ownsolid/ui';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Download } from 'lucide-react';
import { useState } from 'react';
import { useAudience } from '../session.ts';
import { gatewayAliasesQuery } from './key-state.ts';
import { gatewayBaseUrl, sampleCall, sampleLanguages, type SampleLanguage } from './key-usage.ts';

/** The endpoint and sample calls for a key that still works. A stopped key has nothing to call. */
export function KeyUsage({ workspace, gatewayKey }: { workspace: string; gatewayKey: GatewayKey }) {
  const { t } = useI18n();
  const audience = useAudience();
  const aliases = useQuery(gatewayAliasesQuery(audience, workspace));
  const [language, setLanguage] = useState<SampleLanguage>('curl');
  const ca = useMutation({
    mutationFn: (aliasId: string) => downloadGatewayAliasCA(audience, workspace, aliasId),
  });
  const startCa = (aliasId: string) => {
    if (ca.isPending) return;
    ca.mutate(aliasId, {
      onSuccess: ({ content, name }) => {
        saveFile(content, name);
      },
    });
  };
  if (gatewayKey.state !== 'active') return null;
  const alias = aliases.data?.items.find((item) => item.id === gatewayKey.alias_id);
  let body;
  if (aliases.isPending) {
    body = <Skeleton aria-busy className="h-24 w-full" />;
  } else if (!aliases.data) {
    body = isProblem(aliases.error) ? (
      <ProblemNotice
        problem={aliases.error}
        onRetry={() => {
          void aliases.refetch();
        }}
      />
    ) : null;
  } else if (alias?.disabled_at !== null) {
    // A list cut short may simply not reach this key's model, so say nothing rather than guess.
    if (!alias && !aliases.data.complete) return null;
    body = (
      <Notice className="m-0" title={t('gateway.use.gone.title')}>
        {t('gateway.use.gone.detail')}
      </Notice>
    );
  } else {
    const languageName = t(`gateway.use.language.${language}`);
    const missingCa = isProblem(ca.error, 'RESOURCE_NOT_FOUND', 'GATEWAY_AUTHORITY_UNAVAILABLE');
    body = (
      <>
        {alias.available ? null : <Notice className="m-0" title={t('gateway.use.unavailable')} />}
        <DetailList>
          <Detail label={t('gateway.use.baseUrl')}>
            <CopyText text={gatewayBaseUrl(alias.client_origin)} />
          </Detail>
          <Detail label={t('gateway.use.model')}>
            <CopyText text={alias.name} />
          </Detail>
        </DetailList>
        <Button
          className="self-start"
          size="sm"
          aria-disabled={ca.isPending || undefined}
          onClick={() => {
            startCa(alias.id);
          }}
        >
          <Download aria-hidden />
          {t(ca.isPending ? 'gateway.download.pending' : 'gateway.use.ca')}
        </Button>
        {isProblem(ca.error) ? (
          <ProblemNotice
            problem={ca.error}
            title={missingCa ? t('gateway.use.ca.unavailable.title') : undefined}
            detail={missingCa ? t('gateway.use.ca.unavailable.detail') : undefined}
            onRetry={() => {
              startCa(alias.id);
            }}
          />
        ) : null}
        <p className="m-0 text-[13px] text-text-2">{t('gateway.use.environment')}</p>
        <Tabs
          label={t('gateway.use.languages')}
          value={language}
          tabs={sampleLanguages.map((value) => ({
            value,
            label: t(`gateway.use.language.${value}`),
          }))}
          onChange={setLanguage}
        >
          <CodeBlock
            label={t('gateway.use.sample', { language: languageName })}
            code={sampleCall(language, alias.client_origin, alias.name)}
          />
          <p className="m-0 mt-2 text-[13px] text-text-2">{t(`gateway.use.setup.${language}`)}</p>
        </Tabs>
      </>
    );
  }
  return (
    <section aria-labelledby="key-usage" className="flex flex-col gap-3">
      <h3 id="key-usage" className="m-0 text-[14px] font-semibold">
        {t('gateway.use.title')}
      </h3>
      {body}
    </section>
  );
}
