// How much agent traffic control refuses, for the fleet that is overloading it. The level acts at
// once and agents honor the retry hint they are refused with (ADR 0043).
import {
  getAgentShed,
  isProblem,
  updateAgentShed,
  type AgentShed,
  type ShedLevel,
} from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Detail,
  DetailList,
  Notice,
  ProblemNotice,
  SelectField,
  Skeleton,
  Spinner,
  TextField,
  TypedConfirmation,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState, type ReactNode, type SubmitEvent } from 'react';

const shedKey = ['agent-shed'] as const;

const levels: readonly ShedLevel[] = ['none', 'bulk', 'telemetry', 'heartbeats'];

/** Reads the level, for every place that shows it. */
function useAgentShed() {
  return useQuery({ queryKey: shedKey, queryFn: ({ signal }) => getAgentShed({ signal }) });
}

/** Says in place that agents are being refused, for as long as they are. */
function ShedInForce({
  level,
  className,
  actions,
}: {
  level: ShedLevel;
  className: string;
  actions?: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <Notice
      tone="critical"
      className={className}
      title={t('shed.active', { level: t(`shed.level.${level}`) })}
      actions={actions}
    >
      {t(`shed.level.${level}.hint`)}
    </Notice>
  );
}

/**
 * The Nodes page's warning: with heartbeats refused every node turns offline, which reads as an
 * outage to anyone who did not set the level. A failed read shows nothing, since the list still
 * says what it knows.
 */
export function NodesShedNotice() {
  const { t } = useI18n();
  const level = useAgentShed().data?.level;
  if (level === undefined || level === 'none') return null;
  return (
    <ShedInForce
      level={level}
      className="mb-4"
      actions={
        <Link to="/nodes/pacing" className="font-[550] text-text underline">
          {t('shed.review')}
        </Link>
      }
    />
  );
}

function ShedForm({ shed }: { shed: AgentShed }) {
  const { t, format } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [level, setLevel] = useState<ShedLevel>(shed.level);
  const [reason, setReason] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [baseline, setBaseline] = useState(shed.version);
  // What another administrator saved replaces the choice shown.
  if (shed.version !== baseline) {
    setBaseline(shed.version);
    setLevel(shed.level);
  }

  const save = useMutation({
    mutationFn: (next: ShedLevel) => updateAgentShed(next, shed.version, reason.trim()),
    onSuccess: (saved) => {
      queryClient.setQueryData(shedKey, saved);
      setReason('');
      setConfirming(false);
      const previous = shed.level;
      toast({
        message:
          saved.level === 'none'
            ? t('shed.lifted')
            : t('shed.saved', { level: t(`shed.level.${saved.level}`) }),
        undo: async () => {
          queryClient.setQueryData(
            shedKey,
            await updateAgentShed(previous, saved.version, t('pacing.reason.undo')),
          );
        },
      });
    },
    // The next save needs the current version.
    onError: (error) =>
      isProblem(error, 'VERSION_CONFLICT')
        ? queryClient.invalidateQueries({ queryKey: shedKey })
        : undefined,
  });

  const changed = level !== shed.level;
  const ready = changed && reason.trim() !== '' && !save.isPending;
  const failure = save.error && isProblem(save.error) ? save.error : null;
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!ready) return;
    if (level === 'heartbeats') setConfirming(true);
    else save.mutate(level);
  };

  return (
    <>
      <form noValidate onSubmit={submit} className="flex flex-col gap-4">
        {failure && !confirming ? (
          <ProblemNotice
            problem={failure}
            onRetry={() => {
              save.mutate(level);
            }}
          />
        ) : null}
        <DetailList>
          <Detail label={t('shed.current')}>{t(`shed.level.${shed.level}`)}</Detail>
          <Detail label={t('pacing.updated')}>{format.dateTime(shed.updated_at)}</Detail>
        </DetailList>
        <SelectField
          label={t('shed.field')}
          name="level"
          hint={t(`shed.level.${level}.hint`)}
          value={level}
          options={levels.map((value) => ({ value, label: t(`shed.level.${value}`) }))}
          onChange={(event) => {
            setLevel(event.target.value as ShedLevel);
          }}
        />
        <TextField
          label={t('shed.reason')}
          name="shed-reason"
          autoComplete="off"
          maxLength={500}
          required
          hint={t('pacing.reason.hint')}
          value={reason}
          onChange={(event) => {
            setReason(event.target.value);
          }}
        />
        <div>
          <Button type="submit" variant="primary" disabled={!ready}>
            {save.isPending ? <Spinner label={t('shed.apply')} className="text-white" /> : null}
            {t('shed.apply')}
          </Button>
        </div>
      </form>
      {confirming ? (
        <TypedConfirmation
          title={t('shed.confirm.title')}
          warning={t('shed.confirm.warning')}
          consequence={t('shed.level.heartbeats.hint')}
          expected="heartbeats"
          confirmLabel={t('shed.confirm.action')}
          pending={save.isPending}
          onClose={() => {
            setConfirming(false);
          }}
          onConfirm={() => {
            save.mutate('heartbeats');
          }}
        >
          {failure ? <ProblemNotice problem={failure} /> : null}
        </TypedConfirmation>
      ) : null}
    </>
  );
}

/** The pacing page's section: the level in force, and the form that changes it. */
export function AgentShedSection() {
  const { t } = useI18n();
  const shed = useAgentShed();
  let body;
  if (shed.isPending) {
    body = <Skeleton aria-busy className="h-[220px] w-full" />;
  } else if (!shed.data) {
    body = isProblem(shed.error) ? (
      <ProblemNotice
        problem={shed.error}
        onRetry={() => {
          void shed.refetch();
        }}
      />
    ) : null;
  } else {
    body = <ShedForm shed={shed.data} />;
  }
  return (
    <Card className="mt-4">
      <CardHeader title={t('shed.title')} />
      <CardBody>
        <p className="m-0 mb-4 max-w-[760px] text-text-2">{t('shed.subtitle')}</p>
        {shed.data && shed.data.level !== 'none' ? (
          <ShedInForce level={shed.data.level} className="mb-4" />
        ) : null}
        {body}
      </CardBody>
    </Card>
  );
}
