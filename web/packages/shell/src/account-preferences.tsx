// Keeps this browser's theme and density in step with the signed-in account.
import { getPreferences, updatePreferences, type PreferenceChange } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { usePreferences, useToast } from '@ownsolid/ui';
import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo } from 'react';
import { createPreferenceSync } from './preference-sync.ts';
import { useSession } from './session.ts';

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
  const client = useQueryClient();
  const query = useMemo(() => preferencesQuery(userId), [userId]);
  const { data } = useQuery(query);
  // Through a mutation, so a save that finds the session over sends the user to sign in.
  const { mutateAsync } = useMutation({
    mutationFn: ({ change, revision }: { change: PreferenceChange; revision: number }) =>
      updatePreferences(change, revision),
  });
  const sync = useMemo(
    () =>
      createPreferenceSync({
        known: () => client.getQueryData(query.queryKey),
        load: () => client.query(query),
        save: (change, revision) => mutateAsync({ change, revision }),
        onSaved: (saved) => {
          client.setQueryData(query.queryKey, saved);
        },
        onFailed: (_, retry) => {
          toast({ message: t('preferences.saveFailed'), retry });
        },
      }),
    [client, query, mutateAsync, toast, t],
  );

  useEffect(() => connect(sync.save), [connect, sync]);
  useEffect(() => {
    if (data && !sync.busy()) adopt(data);
  }, [data, adopt, sync]);

  return null;
}

/** Follows the signed-in account: its saved values apply here, and changes made here save. */
export function AccountPreferences() {
  const me = useSession();
  return me ? <Sync key={me.user.id} userId={me.user.id} /> : null;
}
