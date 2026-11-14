import { ApiProblem, createJoinToken, isProblem, revokeJoinToken } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  capabilitiesQuery,
  capabilityDetail,
  capabilityName,
  isSignedOut,
  runtimeConfigQuery,
  signInPath,
  useSession,
  useWorkspaces,
} from '@ownsolid/shell';
import {
  AdvancedFold,
  Button,
  Card,
  CardBody,
  Notice,
  ProblemNotice,
  SelectField,
  Skeleton,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useBlocker } from '@tanstack/react-router';
import { ArrowLeft, FileSpreadsheet } from 'lucide-react';
import { useEffect, useRef, useState, type SubmitEvent } from 'react';
import { EnrollmentCommands } from './add-node-dialog.tsx';
import {
  enrollmentFailure,
  enrollmentReceipt,
  findEnrollmentReceipts,
  readEnrollmentDraft,
  retryEnrollment,
  saveEnrollmentDraft,
  type EnrollmentDraft,
  type EnrollmentResult,
} from './node-enrollment-batch.ts';
import {
  enrollmentCsvLimits,
  parseEnrollmentCsv,
  type EnrollmentCsvRow,
} from './node-enrollment-csv.ts';

import { EnrollmentReview, type EnrollmentRevocation } from './node-enrollment-review.tsx';

const concurrency = 3;
function EnrollmentPlatform() {
  const { t } = useI18n();
  const capabilities = useQuery(capabilitiesQuery());
  const choices = [
    { id: 'node-operating-systems', value: 'linux-amd64', label: 'nodes.bulk.os' },
    { id: 'accelerators', value: 'cpu-only', label: 'nodes.bulk.accelerator' },
  ] as const;
  if (capabilities.isPending) return <Skeleton className="h-9 w-full" />;
  if (isProblem(capabilities.error)) {
    return (
      <ProblemNotice problem={capabilities.error} onRetry={() => void capabilities.refetch()} />
    );
  }
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {choices.map(({ id, value, label }) => {
        const registry = capabilities.data?.registries.find((item) => item.id === id);
        if (!registry) return null;
        return (
          <SelectField
            key={id}
            label={t(label)}
            defaultValue={value}
            options={registry.options.map((option) => {
              const available = option.id === value && option.state === 'ready';
              const name = capabilityName(t, registry, option);
              return {
                value: option.id,
                label: available
                  ? name
                  : t('nodes.bulk.unavailableOption', {
                      name,
                      reason:
                        capabilityDetail(t, registry, option) || t('nodes.bulk.readOnlyOption'),
                    }),
                disabled: !available,
              };
            })}
          />
        );
      })}
    </div>
  );
}

function EnrollmentFlow({ draftKey }: { draftKey: string }) {
  const { t, format } = useI18n();
  const workspaces = useWorkspaces();
  const queryClient = useQueryClient();
  const toast = useToast();
  const poll = useQuery(runtimeConfigQuery()).data?.ui.pollMilliseconds ?? false;
  const [draft, setDraft] = useState(() => readEnrollmentDraft(draftKey));
  const draftRef = useRef(draft);
  const [selected, setSelected] = useState<string | null>(null);
  const [fileIssue, setFileIssue] = useState<string | null>(null);
  const [reading, setReading] = useState(false);
  const [issuing, setIssuing] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [revocation, setRevocation] = useState<EnrollmentRevocation | null>(null);
  const [secrets, setSecrets] = useState<Partial<Record<number, string>>>({});
  const [clock, setClock] = useState(() => Date.now());
  const running = useRef<{ stopped: boolean } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadButton = useRef<HTMLButtonElement>(null);

  useBlocker({
    shouldBlockFn: ({ next }) => running.current !== null && next.pathname !== signInPath,
    enableBeforeUnload: () => running.current !== null,
  });
  const workspace = draft?.workspace ?? selected ?? workspaces.active?.id ?? '';
  const workspaceAllowed =
    !workspaces.isPending &&
    !workspaces.error &&
    (workspaces.items.some((item) => item.id === workspace && item.status === 'active') ||
      (workspaces.active?.id === workspace &&
        !workspaces.items.some((item) => item.id === workspace)));
  const started = Boolean(draft && Object.keys(draft.results).length > 0);
  const blockedRows = draft?.rows.some((row) => row.issue) ?? false;
  const queued = draft?.rows.filter((row) => !draft.results[row.line]) ?? [];
  const retries = draft?.rows.filter((row) => retryEnrollment(draft.results[row.line])) ?? [];
  const retryAt = retries.reduce((time, row) => {
    const result = draft?.results[row.line];
    return result?.state === 'failed' ? Math.max(time, result.retryAt ?? 0) : time;
  }, 0);

  useEffect(() => {
    if (retryAt <= clock) return;
    const timer = window.setTimeout(() => {
      setClock(Date.now());
    }, retryAt - Date.now());
    return () => {
      window.clearTimeout(timer);
    };
  }, [retryAt, clock]);

  const update = (next: EnrollmentDraft | null) => {
    draftRef.current = next;
    saveEnrollmentDraft(draftKey, next);
    setDraft(next);
  };
  const results = draft?.results;
  const pending = Object.values(results ?? {}).flatMap((result) =>
    result?.state === 'pending' ? [result.token] : [],
  );
  const progress = useQuery({
    queryKey: ['nodes', 'bulk-enrollment-progress', workspace, pending.map(({ id }) => id)],
    queryFn: ({ signal }) => findEnrollmentReceipts(workspace, pending, signal),
    enabled: workspaceAllowed && !issuing && pending.length > 0,
    refetchInterval: poll,
  });
  useEffect(() => {
    const current = draftRef.current;
    if (current?.workspace !== workspace || !progress.data) return;
    const receipts = new Map(progress.data.map((token) => [token.id, token]));
    const updated = { ...current.results };
    const completed = new Set<number>();
    for (const row of current.rows) {
      const result = updated[row.line];
      if (result?.state !== 'pending') continue;
      const receipt = receipts.get(result.token.id);
      if (!receipt || receipt.status === 'pending') continue;
      updated[row.line] = enrollmentReceipt(receipt);
      completed.add(row.line);
    }
    if (completed.size === 0) return;
    const next = { ...current, results: updated };
    draftRef.current = next;
    saveEnrollmentDraft(draftKey, next);
    setDraft(next);
    setSecrets((previous) =>
      Object.fromEntries(Object.entries(previous).filter(([line]) => !completed.has(Number(line)))),
    );
  }, [draftKey, progress.data, workspace]);

  const mint = useMutation({
    retry: false,
    mutationFn: async ({ row, workspaceId }: { row: EnrollmentCsvRow; workspaceId: string }) => {
      const token = await createJoinToken({ workspaceId, nodeName: row.name });
      if (
        token.workspace_id !== workspaceId ||
        token.node_name !== row.name ||
        token.status !== 'pending'
      ) {
        throw new ApiProblem('RESPONSE_INVALID');
      }
      setSecrets((previous) => ({ ...previous, [row.line]: token.token }));
      return enrollmentReceipt(token);
    },
  });

  const revoke = useMutation({
    retry: false,
    mutationFn: async (target: EnrollmentRevocation) => {
      const token = await revokeJoinToken(target.id, t('nodes.pending.reason'));
      if (
        token.id !== target.id ||
        token.workspace_id !== workspace ||
        token.status !== 'revoked'
      ) {
        throw new ApiProblem('RESPONSE_INVALID');
      }
      return token;
    },
    onSuccess: (token, target) => {
      const current = draftRef.current;
      if (current && token.id === target.id && token.workspace_id === current.workspace) {
        update({
          ...current,
          results: { ...current.results, [target.line]: enrollmentReceipt(token) },
        });
        setSecrets((previous) => ({ ...previous, [target.line]: undefined }));
      }
      toast({ message: t('nodes.pending.revoked', { name: target.name }) });
      setRevocation(null);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['nodes', 'join-tokens'] });
      void queryClient.invalidateQueries({ queryKey: ['nodes', 'bulk-enrollment-progress'] });
    },
  });

  async function loadFile(file: File) {
    setReading(true);
    setFileIssue(null);
    try {
      const parsed =
        file.size > enrollmentCsvLimits.bytes
          ? { issue: 'size' as const }
          : parseEnrollmentCsv(
              new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer()),
            );
      if ('issue' in parsed) {
        setFileIssue(
          t(`nodes.bulk.fileIssue.${parsed.issue}`, {
            line: parsed.line ?? 1,
            count: enrollmentCsvLimits.rows,
            kib: enrollmentCsvLimits.bytes / 1024,
          }),
        );
        return;
      }
      update({ workspace, file: file.name, rows: parsed.rows, results: {} });
    } catch {
      setFileIssue(t('nodes.bulk.fileIssue.read'));
    } finally {
      setReading(false);
    }
  }

  const ready =
    Boolean(draft) &&
    workspaceAllowed &&
    !reading &&
    !fileIssue &&
    !blockedRows &&
    !issuing &&
    (queued.length > 0 || retries.length > 0) &&
    retryAt <= clock;

  async function issue(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready || !draft || running.current) return;
    const workspaceId = workspace;
    if (draft.workspace !== workspaceId) update({ ...draft, workspace: workspaceId });
    const rows = [...retries, ...queued];
    const run = { stopped: false };
    running.current = run;
    setIssuing(true);
    function patch(row: EnrollmentCsvRow, result: EnrollmentResult) {
      const current = draftRef.current;
      if (current) update({ ...current, results: { ...current.results, [row.line]: result } });
    }
    async function worker() {
      while (!run.stopped) {
        const row = rows.shift();
        if (!row) return;
        patch(row, { state: 'issuing' });
        try {
          patch(row, await mint.mutateAsync({ row, workspaceId }));
        } catch (error) {
          const result = enrollmentFailure(error);
          patch(row, result);
          if (
            result.state === 'unknown' ||
            isSignedOut(error) ||
            (result.state === 'failed' &&
              ['rate', 'capacity', 'denied', 'workspace'].includes(result.reason))
          ) {
            run.stopped = true;
          }
        }
      }
    }
    try {
      await Promise.all(Array.from({ length: concurrency }, () => worker()));
    } finally {
      running.current = null;
      setIssuing(false);
      setStopping(false);
      setClock(Date.now());
      void queryClient.invalidateQueries({ queryKey: ['nodes', 'join-tokens'] });
    }
  }

  const denied = Object.values(results ?? {}).some(
    (result) =>
      result?.state === 'failed' && (result.reason === 'denied' || result.reason === 'workspace'),
  );
  const problems = Object.values(results ?? {}).filter(
    (result) => result?.state === 'failed' || result?.state === 'unknown',
  ).length;
  const unverified = Boolean(progress.data && progress.data.length < pending.length);
  const canReplace = !started && !reading && workspaceAllowed;
  const progressDenied = isProblem(progress.error, 'ACCESS_DENIED');
  return (
    <>
      <form onSubmit={(event) => void issue(event)} className="max-w-[62rem] space-y-4">
        <Card aria-labelledby="bulk-source">
          <CardBody className="space-y-3">
            <h2 id="bulk-source" className="m-0 text-base font-semibold">
              {t('nodes.bulk.setup')}
            </h2>
            {workspaces.isPending ? (
              <div aria-busy aria-label={t('nodes.bulk.loading')} className="space-y-2">
                <Skeleton className="h-9 w-full sm:w-72" />
                <Skeleton className="h-5 w-full sm:w-52" />
              </div>
            ) : workspaces.error && isProblem(workspaces.error) ? (
              <ProblemNotice problem={workspaces.error} onRetry={workspaces.retry} />
            ) : (
              <SelectField
                label={t('nodes.add.workspace')}
                value={workspace}
                disabled={started || reading}
                options={[
                  { value: '', label: t('nodes.bulk.chooseWorkspace'), disabled: true },
                  ...(workspace && !workspaces.items.some((item) => item.id === workspace)
                    ? [
                        {
                          value: workspace,
                          label:
                            workspaces.active?.id === workspace
                              ? workspaces.active.name
                              : t('nodes.bulk.workspaceUnavailable'),
                          disabled: !workspaceAllowed,
                        },
                      ]
                    : []),
                  ...workspaces.items.map((item) => ({
                    value: item.id,
                    label:
                      item.status === 'active'
                        ? item.name
                        : `${item.name} · ${t('nodes.bulk.workspaceInactive')}`,
                    disabled: item.status !== 'active',
                  })),
                ]}
                onChange={(event) => {
                  setSelected(event.target.value);
                  if (draft) update({ ...draft, workspace: event.target.value });
                }}
              />
            )}
            {!workspaces.isPending && !workspaces.error && !workspaceAllowed ? (
              <Notice title={t('nodes.bulk.workspaceUnavailable')}>
                {t('nodes.bulk.workspaceHelp')}
              </Notice>
            ) : null}
            <EnrollmentPlatform />
            <SelectField
              label={t('nodes.add.role')}
              defaultValue="read_only"
              hint={t('nodes.bulk.unavailableProfiles')}
              options={[
                { value: 'read_only', label: t('nodes.add.role.read_only') },
                {
                  value: 'docker_inference',
                  label: t('nodes.add.role.docker_inference'),
                  disabled: true,
                },
                {
                  value: 'kubernetes_node',
                  label: t('nodes.add.role.kubernetes_node'),
                  disabled: true,
                },
              ]}
            />
            <p className="m-0 text-xs text-text-3">{t('nodes.bulk.unavailablePlacement')}</p>
          </CardBody>
        </Card>

        <Card aria-labelledby="bulk-csv">
          <CardBody className="space-y-3">
            <h2 id="bulk-csv" className="m-0 text-base font-semibold">
              {t('nodes.bulk.source')}
            </h2>
            {!draft && !reading && !fileIssue && workspaceAllowed ? (
              <div className="flex items-start gap-2.5 text-text-2">
                <FileSpreadsheet aria-hidden className="mt-0.5 size-4 shrink-0 text-accent-ink" />
                <div>
                  <h3 className="m-0 font-semibold text-text">{t('nodes.bulk.empty')}</h3>
                  <p className="m-0">{t('nodes.bulk.emptyDetail')}</p>
                </div>
              </div>
            ) : null}
            {canReplace ? (
              <>
                <input
                  ref={fileInput}
                  id="enrollment-csv"
                  type="file"
                  accept=".csv,text/csv"
                  hidden
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    if (file) void loadFile(file);
                  }}
                />
                <Button
                  ref={uploadButton}
                  variant={draft ? 'secondary' : 'primary'}
                  aria-describedby="bulk-csv-hint"
                  onClick={() => {
                    fileInput.current?.click();
                  }}
                >
                  {t('nodes.bulk.file')}
                </Button>
              </>
            ) : null}
            <p id="bulk-csv-hint" className="m-0 text-text-2">
              {t('nodes.bulk.csvHint', {
                count: enrollmentCsvLimits.rows,
                kib: enrollmentCsvLimits.bytes / 1024,
              })}
            </p>
            {reading ? (
              <Skeleton className="h-12 w-full" aria-busy aria-label={t('nodes.bulk.loading')} />
            ) : null}
            {fileIssue ? (
              <Notice tone="critical" title={t('nodes.bulk.fileError')}>
                {fileIssue}
              </Notice>
            ) : null}
            {blockedRows ? (
              <Notice tone="critical" title={t('nodes.bulk.invalidRows')}>
                {t('nodes.bulk.invalidRowsDetail')}
              </Notice>
            ) : null}
            {draft ? <p className="m-0 font-medium wrap-anywhere">{draft.file}</p> : null}
          </CardBody>
        </Card>

        {draft ? (
          <Card aria-labelledby="bulk-issue" className="scroll-mt-4">
            <CardBody className="space-y-3">
              <h2 id="bulk-issue" className="m-0 text-base font-semibold">
                {t('nodes.bulk.issueTitle')}
              </h2>
              {issuing ? (
                <Notice title={t(stopping ? 'nodes.bulk.stopping' : 'nodes.bulk.issuing')}>
                  {t('nodes.bulk.issuingDetail')}
                </Notice>
              ) : null}
              {denied ? (
                <Notice tone="critical" title={t('nodes.bulk.denied')}>
                  {t('nodes.bulk.deniedDetail')}
                </Notice>
              ) : null}
              {!issuing && problems > 0 ? (
                <Notice title={t('nodes.bulk.partial', { count: problems })}>
                  {t('nodes.bulk.partialDetail')}
                </Notice>
              ) : null}
              {isProblem(progress.error) ? (
                <ProblemNotice
                  problem={progress.error}
                  detail={
                    progressDenied ? t('nodes.bulk.deniedDetail') : t('nodes.bulk.progressError')
                  }
                  onRetry={progressDenied ? undefined : () => void progress.refetch()}
                />
              ) : null}
              {unverified ? (
                <Notice title={t('nodes.bulk.unverified')}>
                  {t('nodes.bulk.unverifiedDetail')}
                </Notice>
              ) : null}
              {Object.values(secrets).some(Boolean) && workspaceAllowed && !progressDenied ? (
                <p className="m-0 text-text-2">{t('nodes.bulk.tokenOnce')}</p>
              ) : null}
              {workspaceAllowed && !progressDenied ? (
                <EnrollmentReview
                  key={draft.file}
                  draft={draft}
                  secrets={secrets}
                  started={started}
                  onRevoke={
                    issuing
                      ? undefined
                      : (target) => {
                          revoke.reset();
                          setRevocation(target);
                        }
                  }
                />
              ) : null}
              {started ? (
                <AdvancedFold label={t('nodes.bulk.commands')}>
                  <p className="mb-3 text-text-2">{t('nodes.bulk.commandDetail')}</p>
                  <EnrollmentCommands origin={window.location.origin} profile="read_only" />
                </AdvancedFold>
              ) : null}
              {queued.length > 0 || retries.length > 0 ? (
                <p className="m-0 text-text-2">
                  {t('nodes.bulk.cost', { count: queued.length + retries.length })}
                </p>
              ) : null}
              {retryAt > clock ? (
                <p className="m-0 text-text-2" aria-live="polite">
                  {t('nodes.bulk.retryAt', { time: format.dateTime(new Date(retryAt)) })}
                </p>
              ) : null}
              <div className="flex flex-wrap items-center gap-2">
                {issuing ? (
                  <Button
                    aria-disabled={stopping}
                    onClick={() => {
                      if (running.current) {
                        running.current.stopped = true;
                        setStopping(true);
                      }
                    }}
                  >
                    {t('nodes.bulk.cancel')}
                  </Button>
                ) : queued.length > 0 || retries.length > 0 ? (
                  <Button type="submit" variant="primary" aria-disabled={!ready}>
                    {t(
                      retries.length > 0
                        ? 'nodes.bulk.retry'
                        : started
                          ? 'nodes.bulk.resume'
                          : 'nodes.bulk.issue',
                    )}
                  </Button>
                ) : null}
                {started && !issuing ? (
                  <>
                    <Button
                      onClick={() => {
                        setSecrets({});
                        update(null);
                        setFileIssue(null);
                        requestAnimationFrame(() => {
                          uploadButton.current?.focus();
                        });
                      }}
                    >
                      {t('nodes.bulk.new')}
                    </Button>
                    <Link to="/nodes" className="text-accent-ink underline">
                      {t('nodes.bulk.manageTokens')}
                    </Link>
                  </>
                ) : null}
              </div>
            </CardBody>
          </Card>
        ) : null}
      </form>
      {revocation ? (
        <TypedConfirmation
          title={t('nodes.bulk.revokeTitle', { name: revocation.name })}
          expected={revocation.name}
          consequence={t('nodes.bulk.revokeConsequence')}
          confirmLabel={t('nodes.pending.revoke')}
          pending={revoke.isPending}
          onClose={() => {
            if (!revoke.isPending) setRevocation(null);
          }}
          onConfirm={() => {
            revoke.mutate(revocation);
          }}
        >
          {isProblem(revoke.error) ? <ProblemNotice problem={revoke.error} /> : null}
        </TypedConfirmation>
      ) : null}
    </>
  );
}

/** Reviews an offline CSV, issues node tokens and follows their Control receipts. */
export function BulkNodeEnrollmentPage() {
  const { t } = useI18n();
  const me = useSession();
  const draftKey = me ? `ownsolid.node-enrollment.v1.${me.user.id}` : null;
  return (
    <>
      <Link
        to="/nodes"
        className="mb-3 inline-flex items-center gap-1.5 text-text-2 hover:text-text"
      >
        <ArrowLeft aria-hidden className="size-4" />
        {t('nodes.title')}
      </Link>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('nodes.bulk.title')}
      </h1>
      <p className="mt-0 mb-5 text-text-2">{t('nodes.bulk.subtitle')}</p>
      {draftKey ? <EnrollmentFlow key={draftKey} draftKey={draftKey} /> : null}
    </>
  );
}
