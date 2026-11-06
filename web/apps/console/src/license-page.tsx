// The license: how the installation stands, what the license allows, and what to send the issuer.
// Standing comes from the server's clock, not the browser's (docs/research/license-page.md).
import {
  downloadLicenseDiagnostics,
  getLicense,
  isProblem,
  licenseDaysUntil,
  renewalNoticeDays,
  type License,
  type LicenseSummary,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { licenseStateLabels } from '@ownsolid/shell';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  CopyText,
  Detail,
  DetailList,
  Meter,
  Notice,
  ProblemNotice,
  saveFile,
  Skeleton,
  Spinner,
  StatusPill,
  type StatusTone,
} from '@ownsolid/ui';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Download } from 'lucide-react';
import { LicenseImport, licenseKey } from './license-import.tsx';

const tones: Record<License['state'], StatusTone> = {
  active: 'good',
  grace: 'warning',
  not_yet_valid: 'warning',
  missing: 'critical',
  expired: 'critical',
  clock_suspect: 'critical',
};

/** What the state means now and what to do next. A healthy license says nothing. */
function Standing({ license }: { license: License }) {
  const { t, format } = useI18n();
  const { current, pending } = license;
  switch (license.state) {
    case 'missing':
      return (
        <Notice tone="critical" title={t('license.standing.missing')}>
          {t('license.standing.missing.next')}
        </Notice>
      );
    case 'not_yet_valid':
      return (
        <Notice
          title={t('license.standing.notYetValid', {
            date: format.date(pending?.not_before ?? ''),
          })}
        >
          {t('license.standing.notYetValid.next')}
        </Notice>
      );
    case 'grace':
      return (
        <Notice
          title={t('license.standing.grace', { date: format.date(current?.expires_at ?? '') })}
        >
          {t('license.standing.grace.next', { date: format.dateTime(current?.grace_until ?? '') })}
        </Notice>
      );
    case 'expired':
      return (
        <Notice tone="critical" title={t('license.standing.expired')}>
          {t('license.standing.expired.next', {
            date: format.dateTime(current?.grace_until ?? ''),
          })}
        </Notice>
      );
    case 'clock_suspect':
      return (
        <Notice tone="critical" title={t('license.standing.clock')}>
          {t('license.standing.clock.next', { date: format.dateTime(license.observed_at) })}
        </Notice>
      );
    case 'active': {
      if (!current || pending) return null;
      const days = licenseDaysUntil(current.expires_at, license);
      if (days > renewalNoticeDays) return null;
      return (
        <Notice title={t('license.standing.renewal', { count: days })}>
          {t('license.standing.renewal.next', { date: format.date(current.expires_at) })}
        </Notice>
      );
    }
  }
}

function Terms({ summary, license }: { summary: LicenseSummary; license: License }) {
  const { t, format } = useI18n();
  const left = licenseDaysUntil(summary.expires_at, license);
  return (
    <DetailList>
      <Detail label={t('license.customer')}>{summary.customer_reference}</Detail>
      <Detail label={t('license.id')}>
        <CopyText text={summary.license_id} />
      </Detail>
      <Detail label={t('license.validFrom')}>{format.dateTime(summary.not_before)}</Detail>
      <Detail label={t('license.expires')}>
        {format.dateTime(summary.expires_at)}
        {left > 0 ? (
          <span className="ml-1.5 text-text-3">{t('license.daysLeft', { count: left })}</span>
        ) : null}
      </Detail>
      <Detail label={t('license.graceUntil')}>{format.dateTime(summary.grace_until)}</Detail>
      <Detail label={t('license.digest')}>
        <CopyText text={summary.artifact_sha256} />
      </Detail>
    </DetailList>
  );
}

function Limits({ license }: { license: License }) {
  const { t, format } = useI18n();
  const { accepted_licenses: accepted, maximum_imports: maximum } = license;
  return (
    <DetailList>
      <Detail label={t('license.newWork')}>
        <StatusPill tone={license.new_business_allowed ? 'good' : 'critical'}>
          {t(license.new_business_allowed ? 'license.newWork.allowed' : 'license.newWork.refused')}
        </StatusPill>
      </Detail>
      <Detail label={t('license.scope')}>{t('license.scope.whole')}</Detail>
      <Detail label={t('license.imports')}>
        <div className="flex items-center gap-2.5">
          <Meter
            accent
            label={t('license.imports')}
            value={(accepted / maximum) * 100}
            className="max-w-[160px]"
          />
          <span className="text-text-2 tabular-nums">
            {t('license.imports.count', {
              accepted: format.number(accepted),
              maximum: format.number(maximum),
            })}
          </span>
        </div>
      </Detail>
    </DetailList>
  );
}

function Installation({ license }: { license: License }) {
  const { t, format } = useI18n();
  return (
    <DetailList>
      <Detail label={t('license.deployment')}>
        <CopyText text={license.deployment_id} />
      </Detail>
      <Detail label={t('license.fingerprint')}>
        <CopyText text={license.public_key_fingerprint} />
      </Detail>
      <Detail label={t('license.observed')}>{format.dateTime(license.observed_at)}</Detail>
    </DetailList>
  );
}

function Status({ license }: { license: License }) {
  const { t } = useI18n();
  return (
    <div className="flex flex-col gap-4">
      <Standing license={license} />
      <div className="grid items-start gap-4 xl:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-4">
          <Card>
            <CardHeader
              title={t('license.current')}
              actions={
                <StatusPill tone={tones[license.state]}>
                  {t(licenseStateLabels[license.state])}
                </StatusPill>
              }
            />
            <CardBody>
              {license.current ? (
                <Terms summary={license.current} license={license} />
              ) : (
                <p className="m-0 text-text-2">{t('license.current.none')}</p>
              )}
            </CardBody>
          </Card>
          {license.pending ? (
            <Card>
              <CardHeader title={t('license.next')} />
              <CardBody>
                <Terms summary={license.pending} license={license} />
              </CardBody>
            </Card>
          ) : null}
          <Card>
            <CardHeader title={t('license.limits')} />
            <CardBody>
              <Limits license={license} />
            </CardBody>
          </Card>
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <Card>
            <CardHeader title={t('license.import')} />
            <CardBody>
              <LicenseImport />
            </CardBody>
          </Card>
          <Card>
            <CardHeader title={t('license.installation')} />
            <CardBody>
              <p className="m-0 mb-3 text-[13px] text-text-2">{t('license.installation.hint')}</p>
              <Installation license={license} />
            </CardBody>
          </Card>
        </div>
      </div>
    </div>
  );
}

/** `/license`: the license's standing, its import and what to send the issuer. */
export function LicensePage() {
  const { t } = useI18n();
  const license = useQuery({
    queryKey: licenseKey,
    queryFn: ({ signal }) => getLicense({ signal }),
  });
  const diagnostics = useMutation({
    mutationFn: () => downloadLicenseDiagnostics(),
    onSuccess: ({ content, name }) => {
      saveFile(content, name);
    },
  });

  let body;
  if (license.isPending) {
    body = <Skeleton aria-busy className="h-[320px] w-full" />;
  } else if (license.data) {
    body = <Status license={license.data} />;
  } else if (isProblem(license.error)) {
    body = (
      <ProblemNotice
        problem={license.error}
        onRetry={() => {
          void license.refetch();
        }}
      />
    );
  }

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
            {t('license.title')}
          </h1>
          <p className="m-0 max-w-[760px] text-text-2">{t('license.subtitle')}</p>
        </div>
        {license.data ? (
          <Button
            disabled={diagnostics.isPending}
            onClick={() => {
              diagnostics.mutate();
            }}
          >
            {diagnostics.isPending ? (
              <Spinner label={t('license.diagnostics')} />
            ) : (
              <Download aria-hidden />
            )}
            {t('license.diagnostics')}
          </Button>
        ) : null}
      </div>
      {isProblem(diagnostics.error) ? (
        <ProblemNotice
          className="mb-4"
          problem={diagnostics.error}
          onRetry={() => {
            diagnostics.mutate();
          }}
        />
      ) : null}
      {body}
    </>
  );
}
