// What the console shows about a single sign-on connector and what can be done to it, in its drawer and on
// its own page.
import {
  isProblem,
  testConnector,
  type ConnectorCheck,
  type ManagedConnector,
  type SsoConfiguration,
} from '@ownsolid/api';
import { useI18n, type TranslationKey } from '@ownsolid/i18n';
import {
  Button,
  CopyText,
  Detail,
  DetailList,
  Notice,
  ProblemNotice,
  Spinner,
  StatusPill,
  useToast,
  type StatusTone,
} from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Pencil, Power, PowerOff, Stethoscope } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import {
  CallbackUrls,
  connectorsKey,
  EditConnectorDialog,
  SwitchConnectorDialog,
} from './sso-dialogs.tsx';

/** The installation's single sign-on configuration. */
export const configurationKey = ['sso', 'admin', 'configuration'] as const;

type State = 'on' | 'off' | 'unapproved';

export const states: Record<State, { tone: StatusTone; label: TranslationKey }> = {
  on: { tone: 'good', label: 'sso.status.on' },
  off: { tone: 'neutral', label: 'sso.status.off' },
  unapproved: { tone: 'warning', label: 'sso.status.unapproved' },
};

/** A connector whose origin left the configuration cannot be used, whatever its switch says. */
export function stateOf(
  connector: ManagedConnector,
  configuration: SsoConfiguration | undefined,
): State {
  if (configuration && !configuration.providers.some((p) => p.origin === connector.origin)) {
    return 'unapproved';
  }
  return connector.enabled ? 'on' : 'off';
}

/** What a check found, in words, with the next step. */
function CheckResult({ check, connector }: { check: ConnectorCheck; connector: ManagedConnector }) {
  const { t, format } = useI18n();
  const passed = check.outcome === 'passed';
  const values = {
    origin: connector.origin,
    url: check.callback_url ?? '',
    code: check.provider_error ?? t('sso.check.noCode'),
  };
  return (
    <Notice tone={passed ? 'info' : 'critical'} title={t(`sso.check.${check.outcome}.title`)}>
      <p className="m-0">{t(`sso.check.${check.outcome}.detail`, values)}</p>
      <p className="m-0 mt-1 text-[12px] text-text-2">
        {t('sso.check.at', { time: format.dateTime(check.checked_at) })}
      </p>
    </Notice>
  );
}

type Opened = 'edit' | 'switch' | null;

/** The parts of a connector's surface, for its drawer or page to place. */
export interface ConnectorSurface {
  buttons: ReactNode;
  /** The notices, the connection check and the details. */
  body: ReactNode;
  dialogs: ReactNode;
}

/** A connector's actions and details. */
export function useConnectorSurface(
  connector: ManagedConnector | null,
  configuration: SsoConfiguration | undefined,
): ConnectorSurface {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<Opened>(null);
  const [lastCheck, setLastCheck] = useState<ConnectorCheck | null>(null);
  const check = useMutation({
    mutationFn: (id: string) => testConnector(id),
    onSuccess: setLastCheck,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: connectorsKey });
  const changed = (message: string) => {
    setDialog(null);
    // A change can make an earlier check say nothing about this connector.
    setLastCheck(null);
    void refresh();
    toast({ message });
  };
  const state = connector ? stateOf(connector, configuration) : null;
  const last = connector && lastCheck?.connector_id === connector.id ? lastCheck : null;
  const usable = configuration?.enabled === true;
  return {
    buttons: connector ? (
      <>
        <Button
          onClick={() => {
            setDialog('edit');
          }}
        >
          <Pencil aria-hidden />
          {t('sso.edit.open')}
        </Button>
        {connector.enabled ? (
          <Button
            variant="danger"
            onClick={() => {
              setDialog('switch');
            }}
          >
            <PowerOff aria-hidden />
            {t('sso.off.open')}
          </Button>
        ) : (
          <Button
            variant="primary"
            aria-disabled={!usable || state === 'unapproved' ? true : undefined}
            onClick={() => {
              if (usable && state !== 'unapproved') setDialog('switch');
            }}
          >
            <Power aria-hidden />
            {t('sso.on.open')}
          </Button>
        )}
      </>
    ) : null,
    body: (
      <>
        {connector && configuration && !usable ? (
          <Notice className="mb-4" title={t('sso.off.installation.title')}>
            {t('sso.off.installation.detail')}
          </Notice>
        ) : null}
        {connector && usable && state === 'unapproved' ? (
          <Notice className="mb-4" tone="critical" title={t('sso.detail.unapproved.title')}>
            {t('sso.detail.unapproved.detail', { origin: connector.origin })}
          </Notice>
        ) : null}
        {connector && state ? (
          <div className="flex flex-col gap-5">
            <section aria-labelledby="sso-check" className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h3 id="sso-check" className="m-0 text-[14px] font-semibold">
                  {t('sso.check.title')}
                </h3>
                <Button
                  disabled={check.isPending || !usable}
                  onClick={() => {
                    check.mutate(connector.id);
                  }}
                >
                  {check.isPending ? (
                    <Spinner label={t('sso.check.running')} />
                  ) : (
                    <Stethoscope aria-hidden />
                  )}
                  {t('sso.check.open')}
                </Button>
              </div>
              <p className="m-0 text-[13px] text-text-2">{t('sso.check.detail')}</p>
              {check.error && isProblem(check.error) ? (
                <ProblemNotice
                  problem={check.error}
                  onRetry={() => {
                    check.mutate(connector.id);
                  }}
                />
              ) : null}
              {last ? <CheckResult check={last} connector={connector} /> : null}
            </section>
            <DetailList>
              <Detail label={t('sso.column.status')}>
                <StatusPill tone={states[state].tone}>{t(states[state].label)}</StatusPill>
              </Detail>
              <Detail label={t('sso.column.origin')}>
                <CopyText text={connector.origin} />
              </Detail>
              <Detail label={t('sso.column.clientId')}>
                <CopyText text={connector.client_id} />
              </Detail>
              <Detail label={t('sso.callbacks.title')}>
                {configuration ? (
                  <CallbackUrls configuration={configuration} connectorId={connector.id} />
                ) : (
                  '-'
                )}
              </Detail>
              <Detail label={t('sso.detail.created')}>
                {format.dateTime(connector.created_at)}
              </Detail>
              <Detail label={t('sso.column.updated')}>
                {format.dateTime(connector.updated_at)}
              </Detail>
              <Detail label={t('sso.detail.id')}>
                <CopyText text={connector.id} />
              </Detail>
            </DetailList>
          </div>
        ) : null}
      </>
    ),
    dialogs: (
      <>
        {connector && dialog === 'edit' ? (
          <EditConnectorDialog
            connector={connector}
            onClose={() => {
              setDialog(null);
            }}
            onSaved={(updated) => {
              changed(t('sso.edit.done', { name: updated.name }));
            }}
          />
        ) : null}
        {connector && dialog === 'switch' ? (
          <SwitchConnectorDialog
            connector={connector}
            onClose={() => {
              setDialog(null);
            }}
            onSwitched={(updated) => {
              changed(
                updated.enabled
                  ? t('sso.on.done', { name: updated.name })
                  : t('sso.off.done', { name: updated.name }),
              );
            }}
          />
        ) : null}
      </>
    ),
  };
}
