// Single sign-on: the provider buttons on the sign-in page and the page providers return to.
import { ApiProblem, listSsoConnectors, startSsoLogin } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Button, Notice, ProblemNotice } from '@ownsolid/ui';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { AuthLayout, FailureNotice } from './auth-layout.tsx';
import { signInPath } from './redirect.ts';
import type { SsoResultSearch } from './sso-search.ts';

const problemCode = /^[A-Z][A-Z0-9_]{0,99}$/;

/** Sends the browser to a provider; an address that is not http(s) is refused. */
function leaveFor(address: string): void {
  const url = new URL(address);
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new ApiProblem('RESPONSE_INVALID');
  }
  window.location.assign(url);
}

/** A button per provider. Password sign-in still works when the list does not load. */
export function SsoButtons() {
  const { t } = useI18n();
  const connectors = useQuery({
    queryKey: ['sso', 'connectors'],
    queryFn: ({ signal }) => listSsoConnectors({ signal }),
  });
  const start = useMutation({
    mutationFn: async (id: string) => {
      leaveFor(await startSsoLogin(id));
    },
  });
  if (!connectors.data?.length) return null;
  return (
    <div className="mt-4 flex flex-col gap-2 border-t border-border pt-4">
      {start.error ? <FailureNotice error={start.error} /> : null}
      {connectors.data.map((connector) => (
        <Button
          key={connector.id}
          className="w-full"
          disabled={start.isPending}
          onClick={() => {
            start.mutate(connector.id);
          }}
        >
          {t('sso.continueWith', { name: connector.name })}
        </Button>
      ))}
    </div>
  );
}

/** `/login/sso-result` and `/account/sso-result`: the outcome of a provider round trip. */
export function SsoResultPage({ search }: { search: SsoResultSearch }) {
  const { t } = useI18n();
  const code = search.error ?? '';
  return (
    <AuthLayout title={t('sso.result.title')}>
      {code === '' ? (
        <Notice title={t('sso.result.linked.title')}>{t('sso.result.linked.detail')}</Notice>
      ) : (
        <ProblemNotice
          problem={new ApiProblem(problemCode.test(code) ? code : 'SSO_FLOW_UNUSABLE')}
        />
      )}
      <Button asChild className="mt-4 w-full">
        <Link to={code === '' ? '/' : signInPath}>
          {code === '' ? t('status.notFound.home') : t('password.backToSignIn')}
        </Link>
      </Button>
    </AuthLayout>
  );
}
