// Keeps this browser's theme, density and language in step with the signed-in account.
import { getPreferences, updatePreferences, type PreferenceChange } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { usePreferences, useToast } from '@ownsolid/ui';
import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useEffectEvent, useMemo, useState } from 'react';
import { createPreferenceSync } from './preference-sync.ts';
import { sessionKey, useSession } from './session.ts';

/**
 * The account's preference. It is always stale, so returning to the window picks up what
 * another browser saved. A failed read keeps the values this browser remembers.
 */
function preferencesQuery(userId: string) {
  return queryOptions({
    queryKey: ['preferences', userId],
    queryFn: ({ signal }) => getPreferences({ signal }),
    staleTime: 0,
  });
}

function Sync({ userId }: { userId: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const { adopt, connect } = usePreferences();
  const { adoptLocale, connectLocale } = useI18n();
  const client = useQueryClient();
  const query = useMemo(() => preferencesQuery(userId), [userId]);
  const { data } = useQuery(query);
  // Through a mutation, so a save that finds the session over sends the user to sign in.
  const { mutateAsync } = useMutation({
    mutationFn: ({ change, revision }: { change: PreferenceChange; revision: number }) =>
      updatePreferences(change, revision),
  });
  const [failedSave, setFailedSave] = useState<{ retry: () => void } | null>(null);
  const sync = useMemo(
    () =>
      createPreferenceSync({
        known: () => client.getQueryData(query.queryKey),
        load: () => client.query(query),
        save: (change, revision) => mutateAsync({ change, revision }),
        onSaved: (saved) => {
          const previous = client.getQueryData(query.queryKey);
          client.setQueryData(query.queryKey, saved);
          // A language is also the account's email language, which moves its version.
          if (previous?.locale !== saved.locale) {
            void client.invalidateQueries({ queryKey: sessionKey });
          }
        },
        onFailed: (_, retry) => {
          setFailedSave({ retry });
        },
      }),
    [client, query, mutateAsync],
  );
  // The queue must outlive a change of language, which may be the change it holds, so it
  // depends on neither the toast nor the translator.
  const showFailure = useEffectEvent((retry: () => void) => {
    toast({ message: t('preferences.saveFailed'), retry });
  });
  useEffect(() => {
    if (failedSave) showFailure(failedSave.retry);
  }, [failedSave]);

  useEffect(
    () =>
      connect(({ singleKeyShortcuts, navigationCollapsed, pinnedDestinations, ...appearance }) => {
        sync.save({
          ...appearance,
          ...(singleKeyShortcuts === undefined ? {} : { single_key_shortcuts: singleKeyShortcuts }),
          ...(navigationCollapsed === undefined
            ? {}
            : { navigation_collapsed: navigationCollapsed }),
          ...(pinnedDestinations === undefined
            ? {}
            : { pinned_destinations: [...pinnedDestinations] }),
        });
      }),
    [connect, sync],
  );
  useEffect(
    () =>
      connectLocale((locale) => {
        sync.save({ locale });
      }),
    [connectLocale, sync],
  );
  useEffect(() => {
    if (!data || sync.busy()) return;
    adopt({
      theme: data.theme,
      density: data.density,
      singleKeyShortcuts: data.single_key_shortcuts,
      navigationCollapsed: data.navigation_collapsed,
      pinnedDestinations: data.pinned_destinations,
    });
    adoptLocale(data.locale);
  }, [data, adopt, adoptLocale, sync]);

  return null;
}

/** Follows the signed-in account: its saved values apply here, and changes made here save. */
export function AccountPreferences() {
  const me = useSession();
  return me ? <Sync key={me.user.id} userId={me.user.id} /> : null;
}
