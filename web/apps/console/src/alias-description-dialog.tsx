// Catalogue text has its own revision. A stale save never discards the administrator's draft.
import { editAliasDescription, isProblem, listModelAliases } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { everyPage, gatewayKeys, pageSize, ReasonField, useIntentKeys } from '@ownsolid/shell';
import { Button, CopyText, Dialog, Notice, ProblemNotice, useToast } from '@ownsolid/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState } from 'react';
import { descriptionInput, hasAliasDescription, type DescribedAlias } from './alias-description.ts';

/** A focused edit on S-205; Portal receives the result through its existing read-only catalogue. */
export function AliasDescriptionDialog({
  alias,
  onClose,
}: {
  alias: DescribedAlias;
  onClose: () => void;
}) {
  const { t, format } = useI18n();
  const toast = useToast();
  const client = useQueryClient();
  const intents = useIntentKeys();
  const id = useId();
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const [draft, setDraft] = useState(alias.description);
  const [reason, setReason] = useState('');
  const [basis, setBasis] = useState(alias);
  const [reviewRequired, setReviewRequired] = useState(false);
  const [reviewed, setReviewed] = useState(false);
  const [gone, setGone] = useState(false);
  const value = descriptionInput(draft);
  const refresh = () =>
    client.invalidateQueries({ queryKey: gatewayKeys.aliases(alias.workspace_id) });
  const readLatest = useMutation({
    mutationFn: () =>
      everyPage((cursor) =>
        listModelAliases('admin', alias.workspace_id, { limit: pageSize, cursor }),
      ),
    onSuccess: (page) => {
      const current = page.items.find((item) => item.id === alias.id);
      if (!current || !hasAliasDescription(current)) {
        setGone(true);
        return;
      }
      setBasis(current);
      setReviewRequired(false);
      setReviewed(true);
      save.reset();
    },
  });
  const save = useMutation({
    mutationFn: (body: {
      description: string;
      expectedDescriptionRevision: number;
      reason: string;
    }) => editAliasDescription(alias.workspace_id, alias.id, body, intents.take(body)),
    onSuccess: (answer, body) => {
      intents.done(body);
      void refresh();
      // A delayed answer must not close or announce success in a subsequently opened surface.
      if (!mounted.current) return;
      const current = answer.description_revision === answer.applied_description_revision;
      // Only undo a change that is still current. The retained key makes an unanswered Undo safe.
      const undoKey = crypto.randomUUID();
      const undoBody = {
        description: basis.description,
        expectedDescriptionRevision: answer.description_revision,
        reason: t('services.gateway.description.undoReason'),
      };
      toast({
        message: t(
          current ? 'services.gateway.description.saved' : 'services.gateway.description.newer',
        ),
        undo:
          current && answer.description === '' && basis.description !== ''
            ? () =>
                editAliasDescription(alias.workspace_id, alias.id, undoBody, undoKey).finally(
                  refresh,
                )
            : undefined,
      });
      onClose();
    },
    onError: (error, body) => {
      if (isProblem(error, 'VERSION_CONFLICT') || isProblem(error, 'IDEMPOTENCY_CONFLICT')) {
        // These are confirmed refusals. Review the latest text before creating another intent.
        intents.done(body);
        setReviewRequired(true);
        setReviewed(false);
      }
    },
  });
  const failure = isProblem(save.error) ? save.error : null;
  const denied = failure?.status === 403 || failure?.status === 404;
  const unknown = failure?.code === 'STORAGE_COMMIT_OUTCOME_UNKNOWN';
  const busy = save.isPending || readLatest.isPending;
  const blocked = denied || gone;
  const ready =
    !busy &&
    !blocked &&
    !reviewRequired &&
    value.problem === null &&
    reason.trim() !== '' &&
    (value.text !== basis.description || unknown);
  const run = () => {
    if (!ready) return;
    save.mutate({
      description: value.text,
      expectedDescriptionRevision: basis.description_revision,
      reason: reason.trim(),
    });
  };
  // Do not use the generic stale-page Reload action: reloading would discard this draft.
  const contextual =
    failure &&
    ['VERSION_CONFLICT', 'IDEMPOTENCY_CONFLICT', 'STORAGE_COMMIT_OUTCOME_UNKNOWN'].includes(
      failure.code,
    );
  return (
    <Dialog
      open
      onClose={onClose}
      title={t('services.gateway.description.edit')}
      description={t('services.gateway.description.context', { name: alias.name })}
      footer={
        <>
          <Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button
            type="submit"
            form={id}
            variant="primary"
            aria-disabled={!ready}
            aria-describedby={!ready ? `${id}-guidance` : undefined}
          >
            {t(unknown ? 'action.retry' : 'services.gateway.description.save')}
          </Button>
        </>
      }
    >
      <form
        id={id}
        noValidate
        className="flex flex-col gap-3.5"
        aria-busy={busy}
        onSubmit={(event) => {
          event.preventDefault();
          run();
        }}
      >
        {contextual ? (
          <Notice
            tone="critical"
            className="m-0"
            title={t(
              unknown
                ? 'problem.STORAGE_COMMIT_OUTCOME_UNKNOWN.title'
                : 'services.gateway.description.conflict',
            )}
          >
            <p>
              {t(
                unknown
                  ? 'services.gateway.description.unknown'
                  : 'services.gateway.description.conflictDetail',
              )}
            </p>
            <p className="mt-1 flex flex-wrap items-center gap-1 text-[12px] text-text-3">
              {t('problem.requestIdLabel')} <CopyText text={failure.requestId} />
            </p>
          </Notice>
        ) : failure ? (
          <ProblemNotice problem={failure} onRetry={run} />
        ) : null}
        {reviewRequired ? (
          <Button
            className="self-start"
            aria-disabled={busy}
            onClick={() => {
              if (!busy) readLatest.mutate();
            }}
          >
            {t('services.gateway.description.review')}
          </Button>
        ) : null}
        {isProblem(readLatest.error) ? (
          <ProblemNotice
            problem={readLatest.error}
            onRetry={() => {
              readLatest.mutate();
            }}
          />
        ) : null}
        {gone ? (
          <Notice tone="critical" className="m-0" title={t('services.gateway.description.gone')}>
            {t('services.gateway.description.goneDetail')}
          </Notice>
        ) : null}
        {reviewed ? (
          <section aria-labelledby={`${id}-current`} className="rounded-field bg-surface-2 p-3">
            <h3 id={`${id}-current`} className="m-0 text-[13px] font-semibold">
              {t('services.gateway.description.current')}
            </h3>
            <p className="m-0 mt-1 whitespace-pre-wrap text-[13px] [overflow-wrap:anywhere]">
              {basis.description || t('services.gateway.description.empty')}
            </p>
            <p className="m-0 mt-2 text-[12.5px] text-text-3">
              {t('services.gateway.description.replace')}
            </p>
          </section>
        ) : null}
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${id}-text`} className="font-medium text-text-2">
            {t('services.gateway.description.label')}
          </label>
          <textarea
            id={`${id}-text`}
            name="description"
            rows={5}
            value={draft}
            readOnly={busy || blocked || unknown}
            aria-invalid={value.problem !== null || undefined}
            aria-describedby={`${id}-hint ${id}-count`}
            className="w-full resize-y rounded-field border border-border-strong bg-surface px-3 py-2 text-text outline-none focus:border-accent focus:shadow-[0_0_0_3px_var(--accent-soft)] aria-invalid:border-critical"
            onChange={(event) => {
              setDraft(event.target.value);
            }}
          />
          <p id={`${id}-hint`} className="m-0 text-[12.5px] text-text-3">
            {t('services.gateway.description.hint')}
          </p>
          <p
            id={`${id}-count`}
            className={
              value.problem
                ? 'm-0 text-[12.5px] text-critical-ink'
                : 'm-0 text-[12.5px] text-text-3'
            }
          >
            {value.problem
              ? t(`services.gateway.description.${value.problem}`)
              : t('services.gateway.description.count', { count: format.number(value.length) })}
          </p>
        </div>
        <fieldset className="m-0 min-w-0 border-0 p-0" disabled={busy || blocked || unknown}>
          <ReasonField value={reason} onChange={setReason} />
        </fieldset>
        <p id={`${id}-guidance`} className="m-0 text-[12.5px] text-text-3">
          {t(
            busy
              ? 'status.loading'
              : blocked
                ? 'services.gateway.description.denied'
                : reviewRequired
                  ? 'services.gateway.description.reviewFirst'
                  : 'services.gateway.description.guidance',
          )}
        </p>
      </form>
    </Dialog>
  );
}
