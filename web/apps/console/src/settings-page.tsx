// Settings for everyone in the tenant: for now, the language of people who have not chosen one.
import {
  getTenantSettings,
  isProblem,
  updateTenantSettings,
  type TenantSettings,
} from '@ownsolid/api';
import { isLocale, supportedLocales, useI18n } from '@ownsolid/i18n';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  ProblemNotice,
  SelectField,
  Skeleton,
  Spinner,
  useToast,
} from '@ownsolid/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState, type SubmitEvent } from 'react';

const settingsKey = ['tenant-settings'] as const;

function LanguageForm({ settings }: { settings: TenantSettings }) {
  const { t, setInstallationLocale } = useI18n();
  const toast = useToast();
  const client = useQueryClient();
  const [value, setValue] = useState(settings.default_locale);
  const [baseline, setBaseline] = useState(settings.default_locale);
  // What another administrator saved replaces what the field shows.
  if (settings.default_locale !== baseline) {
    setBaseline(settings.default_locale);
    setValue(settings.default_locale);
  }
  const save = useMutation({
    mutationFn: () => updateTenantSettings(value, settings.revision),
    onSuccess: (saved) => {
      const apply = (next: TenantSettings) => {
        client.setQueryData(settingsKey, next);
        setInstallationLocale(next.default_locale);
      };
      apply(saved);
      toast({
        message: t('settings.saved'),
        undo: async () => {
          apply(await updateTenantSettings(settings.default_locale, saved.revision));
        },
      });
    },
    // The next save needs the current revision.
    onError: (error) =>
      isProblem(error, 'PREFERENCE_REVISION_CONFLICT')
        ? client.invalidateQueries({ queryKey: settingsKey })
        : undefined,
  });
  const submit = (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    save.mutate();
  };
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4">
      {save.error && isProblem(save.error) ? (
        <ProblemNotice
          problem={save.error}
          onRetry={() => {
            save.mutate();
          }}
        />
      ) : null}
      <SelectField
        label={t('settings.language.default')}
        name="default-language"
        value={value}
        hint={t('settings.language.hint')}
        options={[
          { value: 'auto', label: t('settings.language.auto') },
          ...supportedLocales.map((locale) => ({ value: locale, label: t(`locale.${locale}`) })),
        ]}
        onChange={(event) => {
          const next = event.target.value;
          setValue(isLocale(next) ? next : 'auto');
        }}
      />
      <div>
        <Button
          type="submit"
          variant="primary"
          disabled={save.isPending || value === settings.default_locale}
        >
          {save.isPending ? <Spinner label={t('settings.save')} className="text-white" /> : null}
          {t('settings.save')}
        </Button>
      </div>
    </form>
  );
}

/** `/settings`: what an administrator chooses for everyone. */
export function SettingsPage() {
  const { t } = useI18n();
  const settings = useQuery({
    queryKey: settingsKey,
    queryFn: ({ signal }) => getTenantSettings({ signal }),
  });
  let body;
  if (settings.isPending) {
    body = <Skeleton aria-busy className="h-[120px] w-full" />;
  } else if (settings.error) {
    body = isProblem(settings.error) ? (
      <ProblemNotice
        problem={settings.error}
        onRetry={() => {
          void settings.refetch();
        }}
      />
    ) : null;
  } else {
    body = <LanguageForm settings={settings.data} />;
  }
  return (
    <>
      <h1 className="m-0 mb-1 text-[27px] leading-[1.2] font-[750] tracking-[-0.03em]">
        {t('settings.title')}
      </h1>
      <p className="m-0 mb-6 text-text-2">{t('settings.subtitle')}</p>
      <div className="grid items-start gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader title={t('settings.language.title')} />
          <CardBody>{body}</CardBody>
        </Card>
      </div>
    </>
  );
}
