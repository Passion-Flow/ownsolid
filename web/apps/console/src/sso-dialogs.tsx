// The dialogs of the Single sign-on page: adding a connector, editing it, and turning it on or
// off (docs/research/sso-page.md).
import {
  connectorCallbackUrl,
  createConnector,
  isProblem,
  updateConnector,
  type ManagedConnector,
  type SsoConfiguration,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Checkbox,
  CopyText,
  Dialog,
  Notice,
  ProblemNotice,
  SelectField,
  Spinner,
  TextField,
  TypedConfirmation,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useRef, useState, type SubmitEvent } from 'react';

/** Everything under this key follows a change to a connector. */
export const connectorsKey = ['sso', 'admin', 'connectors'] as const;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** Whether a client ID is one as Gitea issues it: a UUID, compared in lower case. */
function validClientId(value: string): boolean {
  return uuid.test(value.trim().toLowerCase());
}

/** One idempotency key per dialog until its request succeeds, so a retry cannot act twice. */
function useKey(): () => string {
  const key = useRef(crypto.randomUUID());
  return () => key.current;
}

/** Reloads the list after a refused write, so a stale revision is replaced before a retry. */
function useReloadOnError(): () => void {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: connectorsKey });
  };
}

/** The callback URLs a provider must accept for a connector, one per entrance origin. */
export function CallbackUrls({
  configuration,
  connectorId,
}: {
  configuration: SsoConfiguration;
  connectorId: string;
}) {
  const { t } = useI18n();
  if (configuration.callback_origins.length === 0) {
    return <Notice tone="critical" title={t('sso.callbacks.none')} />;
  }
  return (
    <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
      {configuration.callback_origins.map((origin) => (
        <li key={origin}>
          <CopyText text={connectorCallbackUrl(origin, connectorId)} />
        </li>
      ))}
    </ul>
  );
}

function ReasonField({
  value,
  onChange,
  error,
}: {
  value: string;
  onChange: (value: string) => void;
  error?: string | undefined;
}) {
  const { t } = useI18n();
  return (
    <TextField
      error={error}
      label={t('sso.form.reason')}
      name="reason"
      autoComplete="off"
      maxLength={500}
      required
      hint={t('sso.form.reason.hint')}
      value={value}
      onChange={(event) => {
        onChange(event.target.value);
      }}
    />
  );
}

function CredentialFields({
  clientId,
  secret,
  onClientId,
  onSecret,
}: {
  clientId: string;
  secret: string;
  onClientId: (value: string) => void;
  onSecret: (value: string) => void;
}) {
  const { t } = useI18n();
  return (
    <>
      <TextField
        label={t('sso.form.clientId')}
        name="client_id"
        autoComplete="off"
        spellCheck={false}
        required
        className="font-mono"
        error={
          clientId.trim() !== '' && !validClientId(clientId)
            ? t('sso.form.clientId.invalid')
            : undefined
        }
        value={clientId}
        onChange={(event) => {
          onClientId(event.target.value);
        }}
      />
      <TextField
        label={t('sso.form.secret')}
        name="client_secret"
        type="password"
        autoComplete="new-password"
        maxLength={4096}
        required
        hint={t('sso.form.secret.hint')}
        value={secret}
        onChange={(event) => {
          onSecret(event.target.value);
        }}
      />
    </>
  );
}

function Submit({
  form,
  label,
  pending,
  ready,
  danger = false,
}: {
  form: string;
  label: string;
  pending: boolean;
  ready: boolean;
  danger?: boolean;
}) {
  return (
    <Button type="submit" form={form} variant={danger ? 'danger' : 'primary'} disabled={!ready}>
      {pending ? <Spinner label={label} className="text-white" /> : null}
      {label}
    </Button>
  );
}

/** Adds a connector, which starts off so it can be tested before anyone uses it. */
export function AddConnectorDialog({
  configuration,
  onClose,
  onAdded,
}: {
  configuration: SsoConfiguration;
  onClose: () => void;
  onAdded: (connector: ManagedConnector) => void;
}) {
  const { t } = useI18n();
  const key = useKey();
  const [id] = useState(() => crypto.randomUUID());
  const [name, setName] = useState('');
  const [origin, setOrigin] = useState(configuration.providers[0]?.origin ?? '');
  const [clientId, setClientId] = useState('');
  const [secret, setSecret] = useState('');
  const [reason, setReason] = useState('');
  const create = useMutation({
    mutationFn: () =>
      createConnector(
        {
          id,
          name: name.trim(),
          origin,
          clientId: clientId.trim().toLowerCase(),
          clientSecret: secret,
          enabled: false,
          reason: reason.trim(),
        },
        key(),
      ),
    onSuccess: onAdded,
  });
  const ready =
    name.trim() !== '' &&
    origin !== '' &&
    validClientId(clientId) &&
    secret !== '' &&
    reason.trim() !== '' &&
    !create.isPending;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready) create.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('sso.add.title')}
      description={t('sso.add.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Submit
            form="add-connector"
            label={t('sso.add.submit')}
            pending={create.isPending}
            ready={ready}
          />
        </>
      }
    >
      <form id="add-connector" noValidate onSubmit={submit} className="flex flex-col gap-4">
        {isProblem(create.error) ? (
          <ProblemNotice
            problem={create.error}
            onRetry={() => {
              create.mutate();
            }}
          />
        ) : null}
        <TextField
          label={t('sso.form.name')}
          name="name"
          autoComplete="off"
          maxLength={100}
          required
          hint={t('sso.form.name.hint')}
          value={name}
          onChange={(event) => {
            setName(event.target.value);
          }}
        />
        <SelectField
          label={t('sso.form.origin')}
          name="origin"
          hint={t('sso.form.origin.hint')}
          options={configuration.providers.map((provider) => ({
            value: provider.origin,
            label: provider.origin,
          }))}
          value={origin}
          onChange={(event) => {
            setOrigin(event.target.value);
          }}
        />
        <div className="flex flex-col gap-1.5">
          <p className="m-0 text-[13px] font-semibold">{t('sso.callbacks.title')}</p>
          <p className="m-0 text-[13px] text-text-2">{t('sso.callbacks.register')}</p>
          <CallbackUrls configuration={configuration} connectorId={id} />
        </div>
        <CredentialFields
          clientId={clientId}
          secret={secret}
          onClientId={setClientId}
          onSecret={setSecret}
        />
        <ReasonField value={reason} onChange={setReason} />
      </form>
    </Dialog>
  );
}

/** Renames a connector, and replaces its credentials when asked to. */
export function EditConnectorDialog({
  connector,
  onClose,
  onSaved,
}: {
  connector: ManagedConnector;
  onClose: () => void;
  onSaved: (connector: ManagedConnector) => void;
}) {
  const { t } = useI18n();
  const key = useKey();
  const reload = useReloadOnError();
  const [name, setName] = useState(connector.name);
  const [replacing, setReplacing] = useState(false);
  const [clientId, setClientId] = useState('');
  const [secret, setSecret] = useState('');
  const [reason, setReason] = useState('');
  const save = useMutation({
    mutationFn: () =>
      updateConnector(
        connector,
        {
          name: name.trim(),
          enabled: connector.enabled,
          credentials: replacing
            ? { clientId: clientId.trim().toLowerCase(), clientSecret: secret }
            : null,
          reason: reason.trim(),
        },
        key(),
      ),
    onSuccess: onSaved,
    onError: reload,
  });
  const changed = name.trim() !== connector.name || replacing;
  const ready =
    changed &&
    name.trim() !== '' &&
    (!replacing || (validClientId(clientId) && secret !== '')) &&
    reason.trim() !== '' &&
    !save.isPending;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready) save.mutate();
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('sso.edit.title', { name: connector.name })}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Submit
            form="edit-connector"
            label={t('sso.edit.submit')}
            pending={save.isPending}
            ready={ready}
          />
        </>
      }
    >
      <form id="edit-connector" noValidate onSubmit={submit} className="flex flex-col gap-4">
        {isProblem(save.error) ? <ProblemNotice problem={save.error} /> : null}
        <TextField
          label={t('sso.form.name')}
          name="name"
          autoComplete="off"
          maxLength={100}
          required
          value={name}
          onChange={(event) => {
            setName(event.target.value);
          }}
        />
        <label className="flex items-center gap-2 text-[14px]">
          <Checkbox
            name="replace"
            checked={replacing}
            onChange={(event) => {
              setReplacing(event.target.checked);
            }}
          />
          {t('sso.edit.replace')}
        </label>
        {replacing ? (
          <>
            {connector.enabled ? <Notice title={t('sso.edit.replace.signsOut')} /> : null}
            <CredentialFields
              clientId={clientId}
              secret={secret}
              onClientId={setClientId}
              onSecret={setSecret}
            />
          </>
        ) : null}
        <ReasonField value={reason} onChange={setReason} />
      </form>
    </Dialog>
  );
}

/** Changes whether a connector is on, at the revision it was read at. */
function useSwitch(connector: ManagedConnector, onSwitched: (c: ManagedConnector) => void) {
  const key = useKey();
  const reload = useReloadOnError();
  return useMutation({
    mutationFn: (reason: string) =>
      updateConnector(
        connector,
        { name: connector.name, enabled: !connector.enabled, credentials: null, reason },
        key(),
      ),
    onSuccess: onSwitched,
    onError: reload,
  });
}

/**
 * Turns a connector off. Everyone signed in with it is signed out and turning it on again does
 * not bring them back, so the name is typed, as the interaction system asks of what cannot be
 * undone.
 */
function TurnOffDialog({
  connector,
  onClose,
  onSwitched,
}: {
  connector: ManagedConnector;
  onClose: () => void;
  onSwitched: (connector: ManagedConnector) => void;
}) {
  const { t } = useI18n();
  const change = useSwitch(connector, onSwitched);
  const [reason, setReason] = useState('');
  const [tried, setTried] = useState(false);
  return (
    <TypedConfirmation
      title={t('sso.off.title', { name: connector.name })}
      consequence={t('sso.off.detail')}
      expected={connector.name}
      confirmLabel={t('sso.off.submit')}
      pending={change.isPending}
      onClose={onClose}
      onConfirm={() => {
        setTried(true);
        if (reason.trim() !== '') change.mutate(reason.trim());
      }}
    >
      <ReasonField
        value={reason}
        onChange={setReason}
        error={tried && reason.trim() === '' ? t('sso.form.reason.required') : undefined}
      />
      {isProblem(change.error) ? <ProblemNotice problem={change.error} /> : null}
    </TypedConfirmation>
  );
}

function TurnOnDialog({
  connector,
  onClose,
  onSwitched,
}: {
  connector: ManagedConnector;
  onClose: () => void;
  onSwitched: (connector: ManagedConnector) => void;
}) {
  const { t } = useI18n();
  const change = useSwitch(connector, onSwitched);
  const [reason, setReason] = useState('');
  const ready = reason.trim() !== '' && !change.isPending;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (ready) change.mutate(reason.trim());
  };
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('sso.on.title', { name: connector.name })}
      description={t('sso.on.detail')}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Submit
            form="turn-on-connector"
            label={t('sso.on.submit')}
            pending={change.isPending}
            ready={ready}
          />
        </>
      }
    >
      <form id="turn-on-connector" noValidate onSubmit={submit} className="flex flex-col gap-4">
        {isProblem(change.error) ? <ProblemNotice problem={change.error} /> : null}
        <ReasonField value={reason} onChange={setReason} />
      </form>
    </Dialog>
  );
}

/** Turns a connector on, or off, which signs out everyone who signed in with it. */
export function SwitchConnectorDialog(props: {
  connector: ManagedConnector;
  onClose: () => void;
  onSwitched: (connector: ManagedConnector) => void;
}) {
  return props.connector.enabled ? <TurnOffDialog {...props} /> : <TurnOnDialog {...props} />;
}
