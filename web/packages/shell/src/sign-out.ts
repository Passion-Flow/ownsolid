import { signOut } from '@ownsolid/api';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { sessionKey } from './session.ts';

/**
 * Signs out, then removes the old session's cached data and calls `onSignedOut`.
 * When the server cannot be reached the session still exists, so the mutation fails
 * and the caller shows the error instead of pretending the user left.
 */
export function useSignOut(onSignedOut: () => void) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => signOut(),
    onSuccess: () => {
      queryClient.clear();
      queryClient.setQueryData(sessionKey, null);
      onSignedOut();
    },
  });
}
