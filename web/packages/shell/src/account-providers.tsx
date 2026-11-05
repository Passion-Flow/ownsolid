// The account page's sign-in providers: what is linked, what can be, and both changes.
// Both ask for the password, and the server records the reason in the audit log.
import {
  isProblem,
  listSsoBindings,
  listSsoConnectors,
  startSsoLink,
  unlinkSsoBinding,
  type SsoBinding,
  type SsoConnector,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Dialog,
  EmptyState,
  ProblemNotice,
  Skeleton,
  Spinner,
  TextField,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Link2 } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { FailureNotice } from './auth-layout.tsx';
import { leaveFor } from './sso.tsx';

const bindingsKey = ['sso', 'bindings'] as const;

interface ConfirmationDialogProps {
  title: string;
  detail: string;
  submitLabel: string;
  /** What the reason field starts with; the person may change it. */
  initialReason: string;
  onClose: () => void;
  /** Sends the confirmation. */
  onConfirm: (password: string, reason: string) => Promise<unknown>;
}

/** Asks for the password and the audited reason before a change to the sign-in providers. */
function ConfirmationDialog({
  title,
  detail,
  submitLabel,
  initialReason,
  onClose,
  onConfirm,
}: ConfirmationDialogProps) {
  const { t } = useI18n();
  const formId = useId();
  const passwordField = useRef<HTMLInputElement>(null);
  const [password, setPassword] = useState('');
  const [reason, setReason] = useState(initialReason);
  const [showErrors, setShowErrors] = useState(false);
  const confirm = useMutation({ mutationFn: () => onConfirm(password, reason.trim()) });
  // The dialog opened in a child's effect and focused its close button; start at the password.
  useEffect(() => {
    passwordField.current?.focus();
  }, []);
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    setShowErrors(true);
    if (password !== '' && reason.trim() !== '') confirm.mutate();
  };
  const wrongPassword = isProblem(confirm.error, 'SSO_PASSWORD_REQUIRED');
  let passwordError: string | undefined;
  if (showErrors && password === '') passwordError = t('account.password.currentRequired');
  else if (wrongPassword) passwordError = t('problem.SSO_PASSWORD_REQUIRED.title');
  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      description={detail}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button type="submit" form={formId} variant="primary" disabled={confirm.isPending}>
            {confirm.isPending ? <Spinner label={submitLabel} className="text-white" /> : null}
            {submitLabel}
          </Button>
        </>
      }
    >
      <form id={formId} noValidate onSubmit={submit} className="flex flex-col gap-4 py-2">
        {confirm.error && !wrongPassword ? <FailureNotice error={confirm.error} /> : null}
        <TextField
          label={t('account.password.current')}
          type="password"
          name="current-password"
          autoComplete="current-password"
          maxLength={4096}
          required
          ref={passwordField}
          value={password}
          error={passwordError}
          onChange={(event) => {
            setPassword(event.target.value);
          }}
        />
        <TextField
          label={t('account.providers.reason')}
          name="reason"
          autoComplete="off"
          maxLength={500}
          required
          value={reason}
          hint={t('account.providers.reason.hint')}
          error={
            showErrors && reason.trim() === '' ? t('account.providers.reason.required') : undefined
          }
          onChange={(event) => {
            setReason(event.target.value);
          }}
        />
      </form>
    </Dialog>
  );
}

function ProviderRow({ name, meta, action }: { name: string; meta: ReactNode; action: ReactNode }) {
  return (
    <li className="flex items-center gap-3 border-b border-border py-3 last:border-b-0">
      <span className="grid size-[30px] flex-none place-items-center rounded-[9px] bg-surface-2 text-text-2">
        <Link2 aria-hidden className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="m-0 truncate font-semibold">{name}</p>
        <p className="m-0 mt-0.5 text-[12.5px] text-text-3">{meta}</p>
      </div>
      {action}
    </li>
  );
}

/** The sign-in providers card of the account page. */
export function ProvidersCard() {
  const { t, locale } = useI18n();
  const toast = useToast();
  const client = useQueryClient();
  const bindings = useQuery({
    queryKey: bindingsKey,
    queryFn: ({ signal }) => listSsoBindings({ signal }),
  });
  const connectors = useQuery({
    queryKey: ['sso', 'connectors'],
    queryFn: ({ signal }) => listSsoConnectors({ signal }),
  });
  const [linking, setLinking] = useState<SsoConnector | null>(null);
  const [unlinking, setUnlinking] = useState<SsoBinding | null>(null);
  // One key per unlink until it succeeds, so a retried request cannot apply twice.
  const unlinkKey = useRef<string | null>(null);

  const closeUnlink = () => {
    unlinkKey.current = null;
    setUnlinking(null);
  };
  const unlink = async (binding: SsoBinding, password: string, reason: string) => {
    unlinkKey.current ??= crypto.randomUUID();
    await unlinkSsoBinding(binding, password, reason, unlinkKey.current);
    closeUnlink();
    toast({ message: t('account.providers.unlink.done', { name: binding.name }) });
    await client.invalidateQueries({ queryKey: bindingsKey });
  };

  let body: ReactNode;
  if (bindings.isPending || connectors.isPending) {
    body = (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-11 w-full" />
        <Skeleton className="h-11 w-full" />
      </div>
    );
  } else if (bindings.error ?? connectors.error) {
    const failed = bindings.error ?? connectors.error;
    body = isProblem(failed) ? (
      <ProblemNotice
        problem={failed}
        onRetry={() => {
          void bindings.refetch();
          void connectors.refetch();
        }}
      />
    ) : null;
  } else {
    const linked = bindings.data.filter((binding) => binding.active);
    const available = connectors.data.filter(
      (connector) => !linked.some((binding) => binding.connector_id === connector.id),
    );
    const format = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' });
    body =
      linked.length === 0 && available.length === 0 ? (
        <EmptyState icon={KeyRound} title={t('account.providers.empty.title')}>
          {t('account.providers.empty.detail')}
        </EmptyState>
      ) : (
        <ul className="m-0 list-none p-0">
          {linked.map((binding) => (
            <ProviderRow
              key={binding.id}
              name={binding.name}
              meta={
                <>
                  {t('account.providers.subject', { subject: binding.subject })}
                  {' · '}
                  {t('account.providers.linked', {
                    date: format.format(new Date(binding.updated_at)),
                  })}
                </>
              }
              action={
                <Button
                  size="sm"
                  aria-label={t('account.providers.unlink.title', { name: binding.name })}
                  onClick={() => {
                    setUnlinking(binding);
                  }}
                >
                  {t('account.providers.unlink')}
                </Button>
              }
            />
          ))}
          {available.map((connector) => (
            <ProviderRow
              key={connector.id}
              name={connector.name}
              meta={t('account.providers.notLinked')}
              action={
                <Button
                  size="sm"
                  aria-label={t('account.providers.link.title', { name: connector.name })}
                  onClick={() => {
                    setLinking(connector);
                  }}
                >
                  {t('account.providers.link')}
                </Button>
              }
            />
          ))}
        </ul>
      );
  }

  return (
    <Card>
      <CardHeader title={t('account.providers.title')} subtitle={t('account.providers.subtitle')} />
      <CardBody>{body}</CardBody>
      {linking ? (
        <ConfirmationDialog
          title={t('account.providers.link.title', { name: linking.name })}
          detail={t('account.providers.link.detail')}
          submitLabel={t('account.providers.link.submit')}
          initialReason={t('account.providers.link.reason')}
          onClose={() => {
            setLinking(null);
          }}
          onConfirm={async (password, reason) => {
            leaveFor(await startSsoLink(linking.id, password, reason));
          }}
        />
      ) : null}
      {unlinking ? (
        <ConfirmationDialog
          title={t('account.providers.unlink.title', { name: unlinking.name })}
          detail={t('account.providers.unlink.detail')}
          submitLabel={t('account.providers.unlink')}
          initialReason={t('account.providers.unlink.reason')}
          onClose={closeUnlink}
          onConfirm={(password, reason) => unlink(unlinking, password, reason)}
        />
      ) : null}
    </Card>
  );
}
