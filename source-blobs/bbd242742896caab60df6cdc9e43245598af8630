// Issuing, rotating and revoking a gateway key. A new secret is shown once, in the dialog that made
// it; rotating says when the old key stops; revoking names what stops and is confirmed by typing
// (docs/research/gateway-keys.md).
import {
  isProblem,
  issueGatewayKey,
  listModelAliases,
  revokeGatewayKey,
  rotateGatewayKey,
  type GatewayKey,
  type MintedGatewayKey,
} from '@ownsolid/api';
import { useI18n, type Translator } from '@ownsolid/i18n';
import { runtimeConfigQuery } from '@ownsolid/shell';
import {
  Button,
  CopyText,
  Dialog,
  Notice,
  ProblemNotice,
  SelectField,
  Skeleton,
  TextField,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useRef, useState, type SubmitEvent } from 'react';
import { everyPage, pageSize } from './every-page.ts';
import { gatewayKeys, lifetimes, overlaps, type Overlap } from './gateway-key-state.ts';

/**
 * One idempotency key per request body until it is answered, so a retry after a lost answer
 * returns the same key instead of making a second, and a changed form is a new request.
 */
export function useIntentKeys() {
  const keys = useRef(new Map<string, string>());
  return {
    take(intent: unknown): string {
      const name = JSON.stringify(intent);
      const key = keys.current.get(name) ?? crypto.randomUUID();
      keys.current.set(name, key);
      return key;
    },
    done(intent: unknown) {
      keys.current.delete(JSON.stringify(intent));
    },
  };
}

/** The reason every gateway change asks for; it is kept in the audit trail. */
export function ReasonField({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useI18n();
  return (
    <TextField
      label={t('gateway.reason')}
      hint={t('gateway.reason.hint')}
      name="reason"
      autoComplete="off"
      maxLength={500}
      required
      value={value}
      error={value.trim() === '' ? t('gateway.reason.required') : undefined}
      onChange={(event) => {
        onChange(event.target.value);
      }}
    />
  );
}

/** The secret of a key just made, shown this once, or why it cannot be shown. */
function SecretOnce({ minted }: { minted: MintedGatewayKey }) {
  const { t, format } = useI18n();
  if (!minted.secret_available || minted.secret === null) {
    return (
      <Notice title={t('gateway.secret.lost.title')} className="m-0">
        {t('gateway.secret.lost.detail')}
      </Notice>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <Notice title={t('gateway.secret.once')} className="m-0">
        <CopyText text={minted.secret} className="mt-1 break-all" />
      </Notice>
      <p className="m-0 text-[13px] text-text-2">
        {t('gateway.secret.use', {
          model: minted.key.alias_name,
          time: format.dateTime(minted.key.expires_at),
        })}
      </p>
    </div>
  );
}

function SecretFooter({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Button variant="primary" onClick={onClose}>
      {t('gateway.secret.done')}
    </Button>
  );
}

/** A lifetime in words: whole days, or else hours. */
function lifetimeLabel(t: Translator) {
  return (seconds: number) => {
    const days = seconds / 86_400;
    return Number.isInteger(days)
      ? t('gateway.issue.lifetime.days', { count: days })
      : t('gateway.issue.lifetime.hours', { count: Math.max(1, Math.round(seconds / 3_600)) });
  };
}

/** Issues a key for one model of the workspace, `alias` chosen first. Open it fresh each time. */
export function IssueKeyDialog({
  workspace,
  alias: initialAlias,
  onClose,
}: {
  workspace: string;
  alias?: string | undefined;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const policy = useQuery(runtimeConfigQuery()).data?.gatewayKeys;
  // The workspace's models on the gateway; only an available one can take a new key.
  const aliases = useQuery({
    queryKey: gatewayKeys.aliases(workspace),
    queryFn: ({ signal }) =>
      everyPage((cursor) => listModelAliases(workspace, { limit: pageSize, cursor }, { signal })),
  });
  const choices = (aliases.data?.items ?? []).filter(
    (item) => item.available && item.disabled_at === null,
  );
  const [label, setLabel] = useState('');
  const [chosen, setChosen] = useState<string | null>(initialAlias ?? null);
  // A chosen model that cannot take a key now falls back to the first that can.
  const alias = choices.some((item) => item.id === chosen)
    ? (chosen ?? '')
    : (choices[0]?.id ?? '');
  const [lifetime, setLifetime] = useState<number | null>(null);
  const [reason, setReason] = useState('');
  const intents = useIntentKeys();
  const seconds = lifetime ?? policy?.defaultTtlSeconds ?? 0;
  const body = { aliasId: alias, label: label.trim(), ttlSeconds: seconds, reason: reason.trim() };
  const issue = useMutation({
    mutationFn: () =>
      issueGatewayKey(workspace, body, intents.take(body)).then((minted) => {
        intents.done(body);
        return minted;
      }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: gatewayKeys.list(workspace) }),
  });
  const ready = body.label !== '' && alias !== '' && body.reason !== '' && seconds > 0;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready && !issue.isPending) issue.mutate();
  };
  const minted = issue.data;
  const lifetimeName = lifetimeLabel(t);
  return (
    <Dialog
      open
      onClose={onClose}
      title={
        minted ? t('gateway.issue.done', { name: minted.key.label }) : t('gateway.issue.title')
      }
      description={minted ? undefined : t('gateway.issue.detail')}
      footer={
        minted ? (
          <SecretFooter onClose={onClose} />
        ) : (
          <>
            <Button onClick={onClose}>{t('action.cancel')}</Button>
            <Button
              type="submit"
              form="issue-key"
              variant="primary"
              disabled={!ready || issue.isPending || choices.length === 0}
            >
              {t('gateway.issue.submit')}
            </Button>
          </>
        )
      }
    >
      {minted ? (
        <SecretOnce minted={minted} />
      ) : (
        <form id="issue-key" noValidate className="flex flex-col gap-3.5" onSubmit={submit}>
          {isProblem(issue.error) ? (
            <ProblemNotice
              problem={issue.error}
              onRetry={() => {
                issue.mutate();
              }}
            />
          ) : null}
          {isProblem(aliases.error) ? (
            <ProblemNotice
              problem={aliases.error}
              onRetry={() => {
                void aliases.refetch();
              }}
            />
          ) : null}
          {aliases.isPending ? <Skeleton className="h-11 w-full" /> : null}
          {aliases.data && choices.length === 0 ? (
            <Notice title={t('gateway.issue.noModel.title')} className="m-0">
              {t('gateway.issue.noModel.detail')}{' '}
              <Link to="/services" className="text-accent-ink underline">
                {t('gateway.issue.noModel.link')}
              </Link>
            </Notice>
          ) : null}
          <TextField
            label={t('gateway.issue.name')}
            hint={t('gateway.issue.name.hint')}
            name="key-name"
            autoComplete="off"
            maxLength={100}
            required
            value={label}
            onChange={(event) => {
              setLabel(event.target.value);
            }}
          />
          <SelectField
            label={t('gateway.issue.model')}
            hint={t('gateway.issue.model.hint')}
            value={alias}
            disabled={choices.length === 0}
            options={choices.map((item) => ({ value: item.id, label: item.name }))}
            onChange={(event) => {
              setChosen(event.target.value);
            }}
          />
          <SelectField
            label={t('gateway.issue.lifetime')}
            hint={t('gateway.issue.lifetime.hint')}
            value={String(seconds)}
            disabled={!policy}
            options={(policy ? lifetimes(policy) : []).map((value) => ({
              value: String(value),
              label:
                value === policy?.defaultTtlSeconds
                  ? t('gateway.issue.lifetime.default', { lifetime: lifetimeName(value) })
                  : lifetimeName(value),
            }))}
            onChange={(event) => {
              setLifetime(Number(event.target.value));
            }}
          />
          <SelectField
            label={t('gateway.issue.limit')}
            hint={t('gateway.issue.limit.notReady')}
            value="none"
            disabled
            options={[{ value: 'none', label: t('gateway.issue.limit.none') }]}
          />
          <ReasonField value={reason} onChange={setReason} />
        </form>
      )}
    </Dialog>
  );
}

/** Rotates a key: a new secret now, the old one stopping after the chosen overlap. */
export function RotateKeyDialog({
  workspace,
  gatewayKey,
  onClose,
}: {
  workspace: string;
  gatewayKey: GatewayKey;
  onClose: () => void;
}) {
  const { t, format } = useI18n();
  const queryClient = useQueryClient();
  const [overlap, setOverlap] = useState<Overlap>(overlaps[2]);
  const [reason, setReason] = useState('');
  const intents = useIntentKeys();
  const body = { overlapSeconds: overlap, reason: reason.trim() };
  const rotate = useMutation({
    mutationFn: () =>
      rotateGatewayKey(workspace, gatewayKey.id, body, intents.take(body)).then((minted) => {
        intents.done(body);
        return minted;
      }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: gatewayKeys.list(workspace) }),
  });
  // When the dialog opened: the server counts the overlap from the rotation, a moment later.
  const [opened] = useState(() => Date.now());
  const stops = Math.min(opened + overlap * 1000, Date.parse(gatewayKey.expires_at));
  const minted = rotate.data;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (body.reason !== '' && !rotate.isPending) rotate.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={
        minted
          ? t('gateway.rotate.done', { name: gatewayKey.label })
          : t('gateway.rotate.title', { name: gatewayKey.label })
      }
      description={minted ? undefined : t('gateway.rotate.detail')}
      footer={
        minted ? (
          <SecretFooter onClose={onClose} />
        ) : (
          <>
            <Button onClick={onClose}>{t('action.cancel')}</Button>
            <Button
              type="submit"
              form="rotate-key"
              variant="primary"
              disabled={body.reason === '' || rotate.isPending}
            >
              {t('gateway.rotate.submit')}
            </Button>
          </>
        )
      }
    >
      {minted ? (
        <SecretOnce minted={minted} />
      ) : (
        <form id="rotate-key" noValidate className="flex flex-col gap-3.5" onSubmit={submit}>
          {isProblem(rotate.error) ? (
            <ProblemNotice
              problem={rotate.error}
              onRetry={() => {
                rotate.mutate();
              }}
            />
          ) : null}
          <SelectField
            label={t('gateway.rotate.overlap')}
            value={String(overlap)}
            options={overlaps.map((value) => ({
              value: String(value),
              label: t(`gateway.rotate.overlap.${value}`),
            }))}
            onChange={(event) => {
              setOverlap(overlaps.find((value) => String(value) === event.target.value) ?? 0);
            }}
          />
          <Notice
            className="m-0"
            title={
              overlap === 0
                ? t('gateway.rotate.now')
                : t('gateway.rotate.until', { time: format.dateTime(new Date(stops)) })
            }
          />
          <ReasonField value={reason} onChange={setReason} />
        </form>
      )}
    </Dialog>
  );
}

/** Revokes a key at once, after its name is typed. */
export function RevokeKeyDialog({
  workspace,
  gatewayKey,
  onClose,
}: {
  workspace: string;
  gatewayKey: GatewayKey;
  onClose: () => void;
}) {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState('');
  const revoke = useMutation({
    mutationFn: () => revokeGatewayKey(workspace, gatewayKey.id, reason.trim()),
    onSuccess: () => {
      toast({ message: t('gateway.revoke.done', { name: gatewayKey.label }) });
      onClose();
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: gatewayKeys.list(workspace) }),
  });
  return (
    <TypedConfirmation
      title={t('gateway.revoke.title', { name: gatewayKey.label })}
      consequence={
        <>
          <p className="m-0 mb-2">
            {t('gateway.revoke.consequence', { model: gatewayKey.alias_name })}
          </p>
          <p className="m-0">
            {gatewayKey.last_used_at
              ? t('gateway.revoke.lastUsed', { time: format.dateTime(gatewayKey.last_used_at) })
              : t('gateway.revoke.neverUsed')}
          </p>
        </>
      }
      expected={gatewayKey.label}
      confirmLabel={t('gateway.revoke.submit')}
      pending={revoke.isPending}
      onClose={onClose}
      onConfirm={() => {
        if (reason.trim() !== '') revoke.mutate();
      }}
    >
      <ReasonField value={reason} onChange={setReason} />
      {isProblem(revoke.error) ? <ProblemNotice problem={revoke.error} /> : null}
    </TypedConfirmation>
  );
}
