// Importing a license file. A license cannot be taken out of the signed history, so every file is
// inspected first: the page shows what it would do, or which check refused it and what to do
// (docs/research/license-page.md).
import {
  importLicense,
  inspectLicense,
  isProblem,
  licenseDaysUntil,
  maximumLicenseFileBytes,
  type LicenseCheck,
  type LicenseFileTerms,
  type LicenseInspection,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { licenseStatusKey } from '@ownsolid/shell';
import {
  Button,
  cn,
  CopyText,
  Detail,
  DetailList,
  Notice,
  ProblemNotice,
  saveFile,
  Spinner,
  TextField,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Check, Circle, Download, Upload, X } from 'lucide-react';
import { useRef, useState, type DragEvent, type SubmitEvent } from 'react';
import { licenseKey } from './license-standing.ts';

interface Inspected {
  fileName: string;
  artifact: string;
  inspection: LicenseInspection;
}

async function inspectFile(file: File): Promise<Inspected> {
  const artifact = await file.text();
  return { fileName: file.name, artifact, inspection: await inspectLicense(artifact) };
}

const checkIcons = { passed: Check, failed: X, not_checked: Circle } as const;

function Checks({ checks }: { checks: readonly LicenseCheck[] }) {
  const { t } = useI18n();
  return (
    <ul className="m-0 flex list-none flex-col gap-1.5 p-0 text-[13.5px]">
      {checks.map(({ name, result }) => {
        const Icon = checkIcons[result];
        return (
          <li key={name} className="flex items-center gap-2">
            <Icon
              aria-hidden
              className={cn(
                'size-4 flex-none',
                result === 'passed' && 'text-good',
                result === 'failed' && 'text-critical',
                result === 'not_checked' && 'text-text-3',
              )}
            />
            <span className={cn('min-w-0 flex-1', result === 'not_checked' && 'text-text-3')}>
              {t(`license.check.${name}`)}
            </span>
            <span className="text-[12.5px] text-text-3">{t(`license.check.result.${result}`)}</span>
          </li>
        );
      })}
    </ul>
  );
}

function FileTerms({ file }: { file: LicenseFileTerms }) {
  const { t, format } = useI18n();
  return (
    <DetailList>
      <Detail label={t('license.customer')}>{file.customer_reference}</Detail>
      <Detail label={t('license.id')}>
        <CopyText text={file.license_id} />
      </Detail>
      <Detail label={t('license.validFrom')}>{format.dateTime(file.not_before)}</Detail>
      <Detail label={t('license.expires')}>{format.dateTime(file.expires_at)}</Detail>
      <Detail label={t('license.graceUntil')}>{format.dateTime(file.grace_until)}</Detail>
    </DetailList>
  );
}

const refusedCodes = {
  LICENSE_DEPLOYMENT_MISMATCH: 'deployment',
  LICENSE_ID_CONFLICT: 'identity',
  LICENSE_IMPORT_CAPACITY_REQUIRED: 'capacity',
  LICENSE_PREDECESSOR_REQUIRED: 'predecessor',
  LICENSE_PENDING_SUCCESSOR_EXISTS: 'pending',
} as const;

/** The catalog name of a refusal: its code, or the rule or check behind the broader codes. */
function refusalName({ code, detail, checks }: LicenseInspection) {
  if (code === 'LICENSE_RENEWAL_WINDOW_INVALID' && detail !== '')
    return `window.${detail}` as const;
  if (
    code === '' ||
    code === 'LICENSE_ARTIFACT_INVALID' ||
    code === 'LICENSE_RENEWAL_WINDOW_INVALID'
  ) {
    const failed = checks.find((check) => check.result === 'failed')?.name;
    return failed === 'key' || failed === 'signature' || failed === 'terms' ? failed : 'format';
  }
  return refusedCodes[code];
}

function Refused({ inspected }: { inspected: Inspected }) {
  const { t, format } = useI18n();
  const { inspection, fileName } = inspected;
  const { file, state } = inspection;
  const name = refusalName(inspection);
  const report = () => {
    const text = JSON.stringify({ format_version: 1, file_name: fileName, inspection }, null, 2);
    saveFile(new Blob([text], { type: 'application/json' }), 'license-inspection.json');
  };
  return (
    <>
      <Notice
        tone="critical"
        title={t(`license.refused.${name}`)}
        actions={
          <Button size="sm" onClick={report}>
            <Download aria-hidden />
            {t('license.report')}
          </Button>
        }
      >
        {t(`license.refused.${name}.next`, {
          imported: format.number(state.accepted_licenses),
          maximum: format.number(state.maximum_imports),
          date: format.date(state.pending?.not_before ?? ''),
        })}
      </Notice>
      {inspection.code === 'LICENSE_DEPLOYMENT_MISMATCH' && file ? (
        <DetailList>
          <Detail label={t('license.compare.file')}>
            <CopyText text={file.deployment_id} />
          </Detail>
          <Detail label={t('license.compare.here')}>
            <CopyText text={state.deployment_id} />
          </Detail>
        </DetailList>
      ) : null}
      {inspection.key_id !== '' && inspection.key_id !== inspection.trusted_key_id ? (
        <DetailList>
          <Detail label={t('license.compare.file')}>
            <CopyText text={inspection.key_id} />
          </Detail>
          <Detail label={t('license.compare.here')}>
            <CopyText text={inspection.trusted_key_id} />
          </Detail>
        </DetailList>
      ) : null}
      {inspection.detail === 'wrong_predecessor' && file ? (
        <DetailList>
          <Detail label={t('license.compare.file')}>
            <CopyText text={file.replaces_license_id || t('license.compare.none')} />
          </Detail>
          <Detail label={t('license.compare.here')}>
            <CopyText text={state.history_head_license_id} />
          </Detail>
        </DetailList>
      ) : null}
      <Checks checks={inspection.checks} />
    </>
  );
}

function effectOf({ inspection }: Inspected, t: ReturnType<typeof useI18n>['t'], date: string) {
  const { file, state } = inspection;
  if (!file) return '';
  if (licenseDaysUntil(file.not_before, state) > 0) return t('license.effect.later', { date });
  return state.current ? t('license.effect.renews') : t('license.effect.now');
}

/** Choosing a file, inspecting it and importing it. */
export function LicenseImport() {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [fileProblem, setFileProblem] = useState('');
  const [dragging, setDragging] = useState(false);
  const [reason, setReason] = useState('');

  const inspect = useMutation({
    mutationFn: inspectFile,
    onSuccess: ({ inspection }) => {
      queryClient.setQueryData(licenseKey, inspection.state);
    },
  });
  const inspected = inspect.data;

  const save = useMutation({
    mutationFn: ({ artifact, inspection }: Inspected) =>
      importLicense({
        artifact,
        reason: reason.trim(),
        expectedHead: inspection.state.history_head_license_id,
      }),
    onSuccess: (result) => {
      queryClient.setQueryData(licenseKey, result.state);
      void queryClient.invalidateQueries({ queryKey: licenseStatusKey });
      inspect.reset();
      setReason('');
      toast({ message: t('license.imported') });
    },
    // Another administrator got there first, or the rules changed: check the file again.
    onError: (error) => {
      if (isProblem(error) && error.code.startsWith('LICENSE_') && inspect.variables) {
        inspect.mutate(inspect.variables);
      }
    },
  });

  const choose = (file: File | undefined) => {
    setFileProblem('');
    inspect.reset();
    save.reset();
    if (!file) return;
    if (file.size === 0) {
      setFileProblem(t('license.file.empty'));
    } else if (file.size > maximumLicenseFileBytes) {
      setFileProblem(t('license.file.tooLarge', { size: format.bytes(maximumLicenseFileBytes) }));
    } else {
      inspect.mutate(file);
    }
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragging(false);
    choose(event.dataTransfer.files[0]);
  };
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (inspected && reason.trim() !== '' && !save.isPending) save.mutate(inspected);
  };

  const another = (
    <Button
      size="sm"
      onClick={() => {
        input.current?.click();
      }}
    >
      <Upload aria-hidden />
      {t(inspected ? 'license.file.another' : 'license.file.choose')}
    </Button>
  );

  return (
    <div className="flex flex-col gap-4">
      <input
        ref={input}
        hidden
        type="file"
        data-testid="license-file"
        onChange={(event) => {
          choose(event.target.files?.[0]);
          event.target.value = '';
        }}
      />
      {inspected ? (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="min-w-0 font-mono text-[13px] break-all">{inspected.fileName}</span>
          {another}
        </div>
      ) : (
        // The button below is the way in; dropping a file only saves a step for a mouse.
        <div
          role="presentation"
          onDragOver={(event) => {
            event.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => {
            setDragging(false);
          }}
          onDrop={drop}
          className={cn(
            'flex flex-col items-center gap-2.5 rounded-card border border-dashed border-border px-4 py-6 text-center',
            dragging && 'border-accent bg-accent-soft',
          )}
        >
          <p className="m-0 text-[13.5px] text-text-2">{t('license.file.drop')}</p>
          {inspect.isPending ? <Spinner label={t('license.file.checking')} /> : another}
          <p className="m-0 text-[12.5px] text-text-3">
            {t('license.file.hint', { size: format.bytes(maximumLicenseFileBytes) })}
          </p>
        </div>
      )}
      {fileProblem !== '' ? <Notice tone="critical" title={fileProblem} /> : null}
      {isProblem(inspect.error) ? (
        <ProblemNotice
          problem={inspect.error}
          onRetry={() => {
            if (inspect.variables) inspect.mutate(inspect.variables);
          }}
        />
      ) : null}
      {isProblem(save.error) ? (
        <ProblemNotice
          problem={save.error}
          onRetry={() => {
            if (inspected) save.mutate(inspected);
          }}
        />
      ) : null}
      {inspected?.inspection.outcome === 'refused' ? <Refused inspected={inspected} /> : null}
      {inspected?.inspection.outcome === 'installed' ? (
        <Notice title={t('license.installed')}>{t('license.installed.next')}</Notice>
      ) : null}
      {inspected?.inspection.outcome === 'importable' && inspected.inspection.file ? (
        <form noValidate onSubmit={submit} className="flex flex-col gap-4">
          <Notice title={t('license.importable')}>
            {effectOf(inspected, t, format.dateTime(inspected.inspection.file.not_before))}
          </Notice>
          <FileTerms file={inspected.inspection.file} />
          <TextField
            label={t('license.reason')}
            name="reason"
            autoComplete="off"
            maxLength={500}
            required
            hint={t('license.reason.hint')}
            value={reason}
            onChange={(event) => {
              setReason(event.target.value);
            }}
          />
          <p className="m-0 text-[13px] text-text-2">{t('license.permanent')}</p>
          <div>
            <Button
              type="submit"
              variant="primary"
              disabled={reason.trim() === '' || save.isPending}
            >
              {save.isPending ? (
                <Spinner label={t('license.importIt')} className="text-white" />
              ) : null}
              {t('license.importIt')}
            </Button>
          </div>
        </form>
      ) : null}
    </div>
  );
}
