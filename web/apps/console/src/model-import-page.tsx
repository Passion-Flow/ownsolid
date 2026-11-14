// Bringing a model in: choose the bundle, see what it takes, and follow it through upload and
// verification. An upload continues while the person moves around the console, and resumes from
// the server's offset when the same file is chosen again (docs/research/model-library.md).
import {
  ApiProblem,
  cancelModelImport,
  getModelCatalog,
  getModelImport,
  isProblem,
  listModelImports,
  startModelImport,
  verifyModelImport,
  type ModelCatalogAvailability,
  type ModelImport,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { runtimeConfigQuery, useWorkspaces } from '@ownsolid/shell';
import {
  AdvancedFold,
  Button,
  buttonVariants,
  Card,
  Checkbox,
  CodeBlock,
  Meter,
  Notice,
  ProblemNotice,
  Spinner,
  StatusPill,
  StepTimeline,
  TextField,
  TypedConfirmation,
  useToast,
  type StepState,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router';
import { ArrowRight, FileArchive, Upload } from 'lucide-react';
import { useEffect, useRef, useState, type SubmitEvent } from 'react';
import { ModelFacts, totalBytes } from './model-details.tsx';
import { canUploadImport, importState, modelKeys, modelsKey } from './model-state.ts';
import { startTransfer, stopTransfer, useTransfer, type Transfer } from './model-upload.ts';
import { ResourceHeader, ResourceUnavailable } from './resource-page.tsx';

const route = getRouteApi('/authenticated/frame/library/imports/$importId');

// Written while the server clears an import's files, before it reads as cancelled.
const cancellationPending = 'MODEL_CANCELLATION_PENDING';

const finished: readonly ModelImport['state'][] = ['ready', 'cancelled'];

function BundleInstructions({ catalog }: { catalog: ModelCatalogAvailability }) {
  const { t } = useI18n();
  // Keep each path literal when the command is pasted into a POSIX shell.
  const files = catalog.catalog.files
    .map(({ path }) => `'${path.replaceAll("'", "'\\''")}'`)
    .join(' ');
  const tarOptions = `--format=ustar --no-recursion -cf bundle.tar -- ${files}`;
  const python = [
    `python3 - ${files} <<'PY'`,
    'import sys',
    'import tarfile',
    '',
    'with tarfile.open("bundle.tar", "w", format=tarfile.USTAR_FORMAT) as bundle:',
    '    for name in sys.argv[1:]:',
    '        bundle.add(name, recursive=False)',
    'PY',
  ].join('\n');
  const commands = [
    { label: t('models.import.howTo.python'), code: python },
    { label: t('models.import.howTo.gnuMac'), code: `gtar ${tarOptions}` },
    { label: t('models.import.howTo.gnu'), code: `tar ${tarOptions}` },
  ];
  return (
    <AdvancedFold label={t('models.import.howTo')}>
      <div className="flex flex-col gap-3">
        <p className="m-0 text-text-2">{t('models.import.howTo.detail')}</p>
        <p className="m-0 text-text-2">{t('models.import.howTo.tools')}</p>
        {commands.map(({ label, code }) => (
          <div key={label} className="min-w-0">
            <h3 className="m-0 mb-1.5 text-[13px] font-semibold">{label}</h3>
            <CodeBlock
              code={code}
              label={label}
              className="[&_pre]:[overflow-wrap:anywhere] [&_pre]:[word-break:normal]"
            />
          </div>
        ))}
      </div>
    </AdvancedFold>
  );
}

/** A file chooser, with the button that opens it. */
function FilePicker({
  label,
  testId,
  onChoose,
}: {
  label: string;
  testId: string;
  onChoose: ((file: File) => void) | undefined;
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <input
        ref={input}
        hidden
        type="file"
        accept=".tar,application/x-tar"
        data-testid={testId}
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) onChoose?.(file);
          event.target.value = '';
        }}
      />
      <Button
        aria-disabled={onChoose === undefined || undefined}
        onClick={() => {
          if (onChoose) input.current?.click();
        }}
      >
        <FileArchive aria-hidden />
        {label}
      </Button>
    </>
  );
}

function ImportForm({ catalog }: { catalog: ModelCatalogAvailability }) {
  const { t, format } = useI18n();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workspaces = useWorkspaces();
  const workspace = workspaces.active;
  const entry = catalog.catalog;
  const [name, setName] = useState(entry.repository.split('/').pop() ?? entry.repository);
  const [reason, setReason] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [accepted, setAccepted] = useState(false);
  // One key per intent, so a retry after a lost answer finds the same import.
  const key = useRef(crypto.randomUUID());
  const imports = useQuery({
    ...modelKeys.imports(workspace?.id ?? ''),
    queryFn: ({ signal }) => listModelImports(workspace?.id ?? '', { signal }),
    enabled: workspace !== null,
  });
  const blocking = imports.data?.find((item) => !finished.includes(item.state));
  const start = useMutation({
    mutationFn: (bundle: File) =>
      startModelImport(
        {
          workspaceId: workspace?.id ?? '',
          name: name.trim(),
          catalogDigest: catalog.catalog_digest,
          expectedBytes: bundle.size,
          reason: reason.trim(),
        },
        key.current,
      ),
    onSuccess: (created, bundle) => {
      const refresh = () => queryClient.invalidateQueries({ queryKey: modelsKey });
      if (canUploadImport(created)) {
        startTransfer(bundle, created, catalog.chunk_bytes, reason.trim(), () => {
          void refresh();
        });
      }
      void refresh();
      void navigate({ to: '/library/imports/$importId', params: { importId: created.id } });
    },
  });
  const tooSmall = file !== null && file.size < catalog.minimum_bundle_bytes;
  const tooLarge = file !== null && file.size > catalog.maximum_bundle_bytes;
  const usable =
    file !== null &&
    !tooSmall &&
    !tooLarge &&
    name.trim() !== '' &&
    reason.trim() !== '' &&
    accepted &&
    workspace !== null &&
    catalog.storage_available &&
    !blocking &&
    !start.isPending;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (usable) start.mutate(file);
  };
  const finalBytes = totalBytes(entry.files);
  return (
    <form noValidate className="flex max-w-[720px] flex-col gap-4" onSubmit={submit}>
      {catalog.storage_available ? null : (
        <Notice tone="critical" title={t('models.import.noStore.title')}>
          {t('models.import.noStore.detail')}
        </Notice>
      )}
      {blocking ? (
        <Notice title={t('models.import.blocked.title', { name: blocking.name })}>
          {t(
            blocking.error_code === 'MODEL_IMPORT_EXPIRY_PENDING'
              ? 'models.import.expiring.detail'
              : 'models.import.blocked.detail',
          )}{' '}
          <Link
            to="/library/imports/$importId"
            params={{ importId: blocking.id }}
            className="text-accent-ink underline"
          >
            {t('models.import.blocked.open')}
          </Link>
        </Notice>
      ) : null}
      {isProblem(start.error) ? (
        <ProblemNotice
          problem={start.error}
          onRetry={() => {
            if (file) start.mutate(file);
          }}
        />
      ) : null}
      <Card className="p-[18px]">
        <h2 className="m-0 mb-3 text-[15px] font-semibold">{t('models.import.what')}</h2>
        <ModelFacts
          summary={{
            repository: entry.repository,
            revision: entry.revision,
            license: entry.license,
            format: entry.format,
            architecture: entry.architecture,
            modelType: entry.model_type,
            contextTokens: entry.max_context,
            files: entry.files,
            bytes: finalBytes,
          }}
        />
      </Card>
      <Card className="flex flex-col gap-3.5 p-[18px]">
        <h2 className="m-0 text-[15px] font-semibold">{t('models.import.bundle')}</h2>
        <div className="flex flex-wrap items-center gap-3">
          <FilePicker
            label={t('models.import.choose')}
            testId="model-bundle"
            onChoose={(chosen) => {
              setFile(chosen);
            }}
          />
          {file ? (
            <span className="min-w-0 font-mono text-[13px] break-all">
              {file.name} · {format.bytes(file.size)}
            </span>
          ) : (
            <span className="text-text-2">{t('models.import.noFile')}</span>
          )}
        </div>
        {tooSmall ? (
          <Notice tone="critical" title={t('models.import.tooSmall.title')}>
            {t('models.import.tooSmall.detail', {
              minimum: format.bytes(catalog.minimum_bundle_bytes),
            })}
          </Notice>
        ) : null}
        {tooLarge ? (
          <Notice tone="critical" title={t('models.import.tooLarge.title')}>
            {t('models.import.tooLarge.detail', {
              maximum: format.bytes(catalog.maximum_bundle_bytes),
            })}
          </Notice>
        ) : null}
        <BundleInstructions catalog={catalog} />
        <TextField
          label={t('models.column.name')}
          name="name"
          autoComplete="off"
          maxLength={100}
          required
          value={name}
          onChange={(event) => {
            setName(event.target.value);
          }}
        />
        <TextField
          label={t('models.reason')}
          name="reason"
          autoComplete="off"
          maxLength={500}
          required
          hint={t('models.reason.hint')}
          value={reason}
          onChange={(event) => {
            setReason(event.target.value);
          }}
        />
        <label className="flex items-start gap-2.5">
          <Checkbox
            checked={accepted}
            onChange={(event) => {
              setAccepted(event.target.checked);
            }}
          />
          <span>{t('models.import.license', { license: entry.license })}</span>
        </label>
      </Card>
      <Card className="p-[18px]">
        <h2 className="m-0 mb-1 text-[15px] font-semibold">{t('models.import.storage.title')}</h2>
        <p className="m-0 text-text-2" aria-live="polite">
          {file
            ? t('models.import.cost', {
                during: format.bytes(file.size + finalBytes),
                size: format.bytes(finalBytes),
              })
            : t('models.import.cost.none', { size: format.bytes(finalBytes) })}
        </p>
        <p className="m-0 mt-1 text-text-2">{t('models.import.storage.cleanup')}</p>
      </Card>
      <div>
        <Button type="submit" variant="primary" disabled={!usable}>
          {start.isPending ? <Spinner label={t('models.import.submit')} /> : <Upload aria-hidden />}
          {t('models.import.submit')}
        </Button>
      </div>
    </form>
  );
}

/** `/library/import`: what will arrive, the bundle, and what it takes. */
export function ModelImportPage() {
  const { t } = useI18n();
  const catalog = useQuery({
    queryKey: modelKeys.catalog,
    queryFn: ({ signal }) => getModelCatalog({ signal }),
  });
  if (!catalog.data) {
    return (
      <ResourceUnavailable
        pending={catalog.isPending}
        error={catalog.error}
        onRetry={() => {
          void catalog.refetch();
        }}
        backTo="/library"
        backLabel={t('models.page.back')}
        missingTitle={t('models.import.title')}
        missingDetail={t('models.import.detail')}
        failedTitle={t('models.import.failed')}
      />
    );
  }
  return (
    <>
      <ResourceHeader
        backTo="/library"
        backLabel={t('models.page.back')}
        title={t('models.import.title')}
        description={t('models.import.detail')}
        status={null}
        actions={null}
      />
      <ImportForm catalog={catalog.data} />
    </>
  );
}

function stepStates(item: ModelImport, sending: boolean): [StepState, StepState, StepState] {
  if (item.error_code === 'MODEL_IMPORT_EXPIRY_PENDING') return ['cancelled', 'pending', 'pending'];
  switch (item.state) {
    case 'uploading':
      return [sending ? 'running' : 'pending', 'pending', 'pending'];
    case 'queued':
    case 'verifying':
      return ['done', 'running', 'pending'];
    case 'ready':
      return ['done', 'done', 'done'];
    case 'failed':
      return ['done', 'failed', 'pending'];
    case 'cancelled':
      return item.uploaded_bytes < item.expected_bytes
        ? ['cancelled', 'pending', 'pending']
        : ['done', 'cancelled', 'pending'];
  }
}

function Sending({ item, transfer }: { item: ModelImport; transfer: Transfer | undefined }) {
  const { t, format } = useI18n();
  const sent = transfer?.sent ?? item.uploaded_bytes;
  // A row: the meter takes the width the text leaves, and has no height of its own in a column.
  return (
    <div className="flex items-center gap-3">
      <Meter
        accent
        label={t('models.import.progress.label')}
        value={(sent / item.expected_bytes) * 100}
      />
      <p className="m-0 shrink-0 text-text-2 tabular-nums" role="status">
        {t('models.import.progress', {
          sent: format.bytes(sent),
          total: format.bytes(item.expected_bytes),
        })}
      </p>
    </div>
  );
}

/** What to do about an upload that is not running in this tab. */
function Resume({
  item,
  onChoose,
  problem,
}: {
  item: ModelImport;
  onChoose: ((file: File) => void) | undefined;
  problem: string | null;
}) {
  const { t, format } = useI18n();
  return (
    <Notice
      title={t('models.import.resume.title')}
      actions={
        <FilePicker
          label={t('models.import.resume.choose')}
          testId="model-bundle-resume"
          onChoose={onChoose}
        />
      }
    >
      {problem ?? t('models.import.resume.detail', { size: format.bytes(item.expected_bytes) })}
    </Notice>
  );
}

function CancelDialog({ item, onClose }: { item: ModelImport; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState('');
  const cancel = useMutation({
    mutationFn: () => {
      stopTransfer(item.id);
      return cancelModelImport(item.id, reason.trim());
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: modelsKey });
      toast({ message: t('models.import.cancel.done', { name: item.name }) });
      onClose();
    },
  });
  return (
    <TypedConfirmation
      title={t('models.import.cancel.title', { name: item.name })}
      consequence={t('models.import.cancel.consequence')}
      expected={item.name}
      confirmLabel={t('models.import.cancel.submit')}
      pending={cancel.isPending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') cancel.mutate();
      }}
    >
      <TextField
        label={t('models.reason')}
        name="reason"
        autoComplete="off"
        maxLength={500}
        required
        value={reason}
        onChange={(event) => {
          setReason(event.target.value);
        }}
      />
      {isProblem(cancel.error) ? <ProblemNotice problem={cancel.error} /> : null}
    </TypedConfirmation>
  );
}

function Loaded({ item }: { item: ModelImport }) {
  const { t, format } = useI18n();
  const queryClient = useQueryClient();
  const transfer = useTransfer(item.id);
  const [cancelling, setCancelling] = useState(false);
  const [wrongSize, setWrongSize] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: modelsKey });
  const verify = useMutation({
    mutationFn: () => verifyModelImport(item.id, t('models.import.retryReason')),
    onSettled: refresh,
  });
  const stopping = item.error_code === cancellationPending;
  const expiring = item.error_code === 'MODEL_IMPORT_EXPIRY_PENDING';
  const expired = item.state === 'cancelled' && item.error_code === 'MODEL_IMPORT_EXPIRED';
  const uploadOpen = canUploadImport(item);
  useEffect(() => {
    if (!uploadOpen) stopTransfer(item.id);
  }, [item.id, uploadOpen]);
  const sending = transfer?.state === 'sending';
  // A refusal that will not pass needs another file; one that may pass offers Try again instead.
  const stuck =
    transfer?.state === 'failed' && !(isProblem(transfer.problem) && transfer.problem.retryable);
  const canResume = uploadOpen && (transfer === undefined || stuck);
  const needsCatalog = canResume || item.state === 'failed';
  const catalog = useQuery({
    queryKey: modelKeys.catalog,
    queryFn: ({ signal }) => getModelCatalog({ signal }),
    enabled: needsCatalog,
  });
  const rules = catalog.data;
  const state = importState(item);
  const [upload, verification, publication] = stepStates(item, sending);
  const uploadIncomplete = uploadOpen && !sending;
  const publishedModelId = item.state === 'ready' ? item.model_id : null;
  const failure = item.state === 'failed' && item.error_code ? item.error_code : null;
  const resume = (file: File) => {
    setWrongSize(file.size !== item.expected_bytes);
    if (rules && file.size === item.expected_bytes) {
      startTransfer(file, item, rules.chunk_bytes, t('models.import.resumeReason'), () => {
        void refresh();
      });
    }
  };
  return (
    <>
      <ResourceHeader
        backTo="/library"
        backSearch={{ tab: 'imports' }}
        backLabel={t('models.imports.back')}
        title={item.name}
        status={
          <StatusPill tone={state.tone}>
            {t(uploadIncomplete ? 'models.import.resume.incomplete' : state.label)}
          </StatusPill>
        }
        actions={
          publishedModelId ? (
            <Link
              to="/library/$modelId"
              params={{ modelId: publishedModelId }}
              className={buttonVariants({ variant: 'primary' })}
            >
              {t('models.import.openModel')}
              <ArrowRight aria-hidden />
            </Link>
          ) : expired ? (
            <Link to="/library/import" className={buttonVariants({ variant: 'primary' })}>
              {t('models.import.expired.restart')}
              <Upload aria-hidden />
            </Link>
          ) : expiring ? (
            <Button variant="danger" aria-disabled aria-describedby="import-upload-closed">
              {t('models.import.cancel.open')}
            </Button>
          ) : finished.includes(item.state) ? null : (
            <Button
              variant="danger"
              onClick={() => {
                setCancelling(true);
              }}
            >
              {t('models.import.cancel.open')}
            </Button>
          )
        }
      />
      <div className="flex max-w-[720px] flex-col gap-4">
        {stopping ? <Notice title={t('models.import.stopping')} /> : null}
        {expiring || expired ? (
          <Notice
            title={t(expired ? 'models.import.expired.title' : 'models.import.expiring.title')}
            actions={
              <Button aria-disabled aria-describedby="import-upload-closed">
                {t('models.import.resume.unavailable')}
              </Button>
            }
          >
            <p id="import-upload-closed" className="m-0">
              {t(expired ? 'models.import.expired.detail' : 'models.import.expiring.detail')}
            </p>
            <p className="m-0 mt-2 tabular-nums">
              {t('models.import.progress', {
                sent: format.bytes(item.uploaded_bytes),
                total: format.bytes(item.expected_bytes),
              })}
            </p>
          </Notice>
        ) : null}
        {needsCatalog && !rules ? (
          catalog.isPending ? (
            <p role="status" className="m-0 text-text-2">
              {t('models.import.controls.loading')}
            </p>
          ) : isProblem(catalog.error) ? (
            <ProblemNotice
              problem={catalog.error}
              detail={catalog.error.retryable ? t('models.import.controls.unavailable') : undefined}
              onRetry={() => {
                void catalog.refetch();
              }}
            />
          ) : null
        ) : null}
        {uploadOpen ? (
          <Card className="flex flex-col gap-3 p-[18px]">
            <Sending item={item} transfer={transfer} />
            {transfer?.state === 'failed' && isProblem(transfer.problem) ? (
              <ProblemNotice problem={transfer.problem} onRetry={transfer.retry} />
            ) : null}
            {canResume ? (
              <Resume
                item={item}
                onChoose={rules ? resume : undefined}
                problem={wrongSize ? t('models.import.resume.wrongSize') : null}
              />
            ) : null}
            {sending ? (
              <p className="m-0 text-[12.5px] text-text-3">{t('models.import.keepOpen')}</p>
            ) : null}
          </Card>
        ) : null}
        {failure ? <ProblemNotice problem={new ApiProblem(failure, { status: 409 })} /> : null}
        {isProblem(verify.error) ? <ProblemNotice problem={verify.error} /> : null}
        {item.state === 'failed' ? (
          !rules || item.attempts < rules.maximum_attempts ? (
            <div>
              <Button
                variant="primary"
                disabled={verify.isPending}
                aria-disabled={!rules || undefined}
                onClick={() => {
                  if (rules) verify.mutate();
                }}
              >
                {rules
                  ? t('models.import.verifyAgain', {
                      attempt: item.attempts + 1,
                      maximumAttempts: rules.maximum_attempts,
                    })
                  : t('models.import.verifyAgain.action')}
              </Button>
            </div>
          ) : (
            <Notice title={t('models.import.outOfAttempts')} />
          )
        ) : null}
        {publishedModelId ? (
          <Notice title={t('models.import.published.title')}>
            {t('models.import.published.detail')}
          </Notice>
        ) : null}
        <Card className="p-[18px]">
          <StepTimeline
            label={t('models.import.steps')}
            steps={[
              { id: 'upload', title: t('models.import.step.upload'), state: upload },
              {
                id: 'verify',
                title: t('models.import.step.verify'),
                detail: t('models.import.step.verify.detail'),
                state: verification,
              },
              { id: 'publish', title: t('models.import.step.publish'), state: publication },
            ]}
          />
        </Card>
      </div>
      {cancelling && !expiring && !finished.includes(item.state) ? (
        <CancelDialog
          item={item}
          onClose={() => {
            setCancelling(false);
          }}
        />
      ) : null}
    </>
  );
}

/** `/library/imports/$importId`: one import, followed until it ends. */
export function ModelImportStatusPage() {
  const { t } = useI18n();
  const { importId } = route.useParams();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const item = useQuery({
    queryKey: modelKeys.import(importId),
    queryFn: ({ signal }) => getModelImport(importId, { signal, background: true }),
    refetchInterval: (query) =>
      (query.state.data && finished.includes(query.state.data.state)) ||
      (isProblem(query.state.error) && [403, 404].includes(query.state.error.status))
        ? false
        : poll,
    retry: (failures, error) => failures < 2 && isProblem(error) && error.retryable,
  });
  const denied = isProblem(item.error) && [403, 404].includes(item.error.status);
  useEffect(() => {
    if (denied) stopTransfer(importId);
  }, [denied, importId]);
  if (item.data && !denied) {
    return (
      <>
        {isProblem(item.error) ? (
          <ProblemNotice
            problem={item.error}
            onRetry={() => {
              void item.refetch();
            }}
          />
        ) : null}
        <Loaded key={item.data.id} item={item.data} />
      </>
    );
  }
  return (
    <ResourceUnavailable
      pending={item.isPending}
      error={item.error}
      onRetry={() => {
        void item.refetch();
      }}
      backTo="/library"
      backLabel={t('models.imports.back')}
      missingTitle={t('models.import.missing.title')}
      missingDetail={t('models.import.missing.detail')}
      failedTitle={t('models.import.failed')}
    />
  );
}
