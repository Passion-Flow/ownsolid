// How often the fleet's agents report and how they back off. A change reaches every agent with its
// next heartbeat reply. The page says what the online window allows before control refuses it
// (docs/research/nodes-page.md).
import {
  getAgentPacing,
  isProblem,
  slowestHeartbeat,
  updateAgentPacing,
  type AgentPacing,
  type PacingValues,
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
  Skeleton,
  Spinner,
  TextField,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { ArrowLeft } from 'lucide-react';
import { useState, type SubmitEvent } from 'react';

const pacingKey = ['agent-pacing'] as const;

type Field = keyof PacingValues;

/** The protocol's bounds of each value (api/agent/v1/pacing.go). */
const bounds: Record<Field, { minimum: number; maximum: number }> = {
  heartbeat_seconds: { minimum: 5, maximum: 120 },
  jitter_percent: { minimum: 0, maximum: 50 },
  reconnect_base_seconds: { minimum: 1, maximum: 60 },
  reconnect_max_seconds: { minimum: 1, maximum: 900 },
};

const fields: readonly Field[] = [
  'heartbeat_seconds',
  'jitter_percent',
  'reconnect_base_seconds',
  'reconnect_max_seconds',
];

const valuesOf = (pacing: AgentPacing): PacingValues => ({
  heartbeat_seconds: pacing.heartbeat_seconds,
  jitter_percent: pacing.jitter_percent,
  reconnect_base_seconds: pacing.reconnect_base_seconds,
  reconnect_max_seconds: pacing.reconnect_max_seconds,
});

/** A whole number within the field's bounds, or null. */
function parse(field: Field, text: string): number | null {
  if (!/^\d+$/.test(text.trim())) return null;
  const value = Number(text);
  const { minimum, maximum } = bounds[field];
  return value >= minimum && value <= maximum ? value : null;
}

function PacingForm({ pacing }: { pacing: AgentPacing }) {
  const { t } = useI18n();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [texts, setTexts] = useState<Record<Field, string>>(() => {
    const values = valuesOf(pacing);
    return Object.fromEntries(fields.map((field) => [field, String(values[field])])) as Record<
      Field,
      string
    >;
  });
  const [reason, setReason] = useState('');
  const [baseline, setBaseline] = useState(pacing.version);
  // What another administrator saved replaces what the fields show.
  if (pacing.version !== baseline) {
    setBaseline(pacing.version);
    const values = valuesOf(pacing);
    setTexts(
      Object.fromEntries(fields.map((field) => [field, String(values[field])])) as Record<
        Field,
        string
      >,
    );
  }

  const parsed = Object.fromEntries(
    fields.map((field) => [field, parse(field, texts[field])]),
  ) as Record<Field, number | null>;
  const errors: Partial<Record<Field, string>> = {};
  for (const field of fields) {
    if (parsed[field] === null) errors[field] = t('pacing.field.range', bounds[field]);
  }
  const base = parsed.reconnect_base_seconds;
  const most = parsed.reconnect_max_seconds;
  if (base !== null && most !== null && most < base) {
    errors.reconnect_max_seconds = t('pacing.field.maxBelowBase');
  }
  const jitter = parsed.jitter_percent;
  const slowest = slowestHeartbeat(pacing.online_window_seconds, jitter ?? 0);
  const heartbeat = parsed.heartbeat_seconds;
  const tooSlow = heartbeat !== null && jitter !== null && heartbeat > slowest;
  const valid = Object.keys(errors).length === 0 && !tooSlow;
  const values = valid ? (parsed as PacingValues) : null;
  const changed =
    values !== null && fields.some((field) => values[field] !== valuesOf(pacing)[field]);

  const save = useMutation({
    mutationFn: (next: PacingValues) => updateAgentPacing(next, pacing.version, reason.trim()),
    onSuccess: (saved) => {
      queryClient.setQueryData(pacingKey, saved);
      setReason('');
      const previous = valuesOf(pacing);
      toast({
        message: t('pacing.saved'),
        undo: async () => {
          queryClient.setQueryData(
            pacingKey,
            await updateAgentPacing(previous, saved.version, t('pacing.reason.undo')),
          );
        },
      });
    },
    // The next save needs the current version.
    onError: (error) =>
      isProblem(error, 'VERSION_CONFLICT')
        ? queryClient.invalidateQueries({ queryKey: pacingKey })
        : undefined,
  });
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (values && changed && reason.trim() !== '' && !save.isPending) save.mutate(values);
  };

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4">
      {save.error && isProblem(save.error) ? (
        <ProblemNotice
          problem={save.error}
          onRetry={() => {
            if (values) save.mutate(values);
          }}
        />
      ) : null}
      <div className="grid gap-4 sm:grid-cols-2">
        {fields.map((field) => (
          <TextField
            key={field}
            label={t(`pacing.field.${field}`)}
            name={field}
            inputMode="numeric"
            autoComplete="off"
            required
            hint={t(`pacing.field.${field}.hint`)}
            error={errors[field]}
            value={texts[field]}
            onChange={(event) => {
              setTexts({ ...texts, [field]: event.target.value });
            }}
          />
        ))}
      </div>
      {tooSlow ? (
        <Notice tone="critical" title={t('pacing.window.exceeded', { seconds: slowest })}>
          {t('pacing.window.next', { window: pacing.online_window_seconds })}
        </Notice>
      ) : (
        <p className="m-0 text-[13px] text-text-2">
          {t('pacing.window.allowed', {
            seconds: slowest,
            jitter: jitter ?? 0,
            window: pacing.online_window_seconds,
          })}
        </p>
      )}
      <TextField
        label={t('pacing.reason')}
        name="reason"
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
        <Button
          type="submit"
          variant="primary"
          disabled={!changed || reason.trim() === '' || save.isPending}
        >
          {save.isPending ? <Spinner label={t('pacing.save')} className="text-white" /> : null}
          {t('pacing.save')}
        </Button>
      </div>
    </form>
  );
}

function Current({ pacing }: { pacing: AgentPacing }) {
  const { t, format } = useI18n();
  return (
    <DetailList>
      <Detail label={t('pacing.field.heartbeat_seconds')}>
        {t('pacing.seconds', { count: pacing.heartbeat_seconds })}
      </Detail>
      <Detail label={t('pacing.field.jitter_percent')}>
        {format.percent(pacing.jitter_percent / 100)}
      </Detail>
      <Detail label={t('pacing.field.reconnect_base_seconds')}>
        {t('pacing.seconds', { count: pacing.reconnect_base_seconds })}
      </Detail>
      <Detail label={t('pacing.field.reconnect_max_seconds')}>
        {t('pacing.seconds', { count: pacing.reconnect_max_seconds })}
      </Detail>
      <Detail label={t('pacing.window')}>
        {t('pacing.seconds', { count: pacing.online_window_seconds })}
      </Detail>
      <Detail label={t('pacing.updated')}>{format.dateTime(pacing.updated_at)}</Detail>
    </DetailList>
  );
}

/** `/nodes/pacing`: how often agents report, changed with a reason. */
export function AgentPacingPage() {
  const { t } = useI18n();
  const pacing = useQuery({
    queryKey: pacingKey,
    queryFn: ({ signal }) => getAgentPacing({ signal }),
  });
  let current;
  let form;
  if (pacing.isPending) {
    current = <Skeleton aria-busy className="h-[160px] w-full" />;
    form = <Skeleton aria-busy className="h-[260px] w-full" />;
  } else if (!pacing.data) {
    current = isProblem(pacing.error) ? (
      <ProblemNotice
        problem={pacing.error}
        onRetry={() => {
          void pacing.refetch();
        }}
      />
    ) : null;
    form = null;
  } else {
    current = <Current pacing={pacing.data} />;
    form = <PacingForm pacing={pacing.data} />;
  }
  return (
    <>
      <Link
        to="/nodes"
        className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-text-2 hover:text-text"
      >
        <ArrowLeft aria-hidden className="size-4" />
        {t('pacing.back')}
      </Link>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('pacing.title')}
      </h1>
      <p className="m-0 mb-6 max-w-[760px] text-text-2">{t('pacing.subtitle')}</p>
      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
        <Card>
          <CardHeader title={t('pacing.current')} />
          <CardBody>{current}</CardBody>
        </Card>
        {form ? (
          <Card>
            <CardHeader title={t('pacing.change')} />
            <CardBody>{form}</CardBody>
          </Card>
        ) : null}
      </div>
    </>
  );
}
