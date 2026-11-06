# Single sign-on page

Research behind IAM-117: the administrator's page for single sign-on connectors, on the connector API
the control plane already serves.

## What the server offers

The connector API (`api/openapi/v1/sso`) lists, creates and updates connectors for one provider kind,
Gitea, with OAuth 2.0 authorization code and S256 PKCE. A connector names an origin, a client ID (a
UUID, as Gitea issues them) and a client secret, which the server seals with its keyring and never
returns. Updates carry the revision they were read at and a reason; there is no delete.

The origins a connector may use are not free text: the installation's typed configuration lists the
approved providers (`sso.providers`: origin, issuer, dial addresses, CA file), and a connector for any
other origin is refused (`SSO_PROVIDER_DESTINATION_REJECTED`). When the configuration turns single
sign-on off, creating or enabling a connector is refused (`SSO_DISABLED`) while the list still reads.

Disabling a connector, or replacing its client ID and secret, raises its `auth_version`. Every session
that started through it then fails its source check and ends (`source_repository.go`). Enabling it
again does not bring those sessions back.

The page needs two things the API did not have, added to the same contract:

- `GET /api/v1/admin/sso/configuration`: whether single sign-on is on, the approved provider origins,
  and the origins whose callback URLs a provider must accept. Without it the form cannot offer the
  origins or say why adding is refused.
- `POST /api/v1/admin/sso/connectors/{id}/tests`: a check of a stored connector, below.

## How others solve it

- **Auth0** ([test connections](https://auth0.com/docs/authenticate/identity-providers/test-connections)):
  "Try Connection" runs a real sign-in with the provider and shows the profile it returned on an
  "It Works!" page.
- **Cloudflare One** ([identity providers](https://developers.cloudflare.com/cloudflare-one/integrations/identity-providers/)):
  "Test" next to each provider "will attempt to connect to the IdP to verify if a valid connection is
  established", with a page for each outcome.
- **Grafana** ([configure authentication](https://grafana.com/docs/grafana/latest/setup-grafana/configure-security/configure-authentication/)):
  providers are configured in the UI with client ID and secret, and enabled or disabled one by one.
- **Gitea as the provider** ([OAuth2 provider](https://docs.gitea.com/development/oauth2-provider)): an
  application has a name and redirect URIs, and Gitea issues its client ID and secret. So the redirect
  URI has to be known before the client ID exists.

## The test

A real sign-in, as Auth0 and Cloudflare do, needs a flow mode that signs nobody in; the flow code is
the most guarded in the module, so that is a separate item (IAM-118). The test here checks everything a
sign-in needs that the server can check alone, and says what it did not check.

The first idea was token introspection ([RFC 7662](https://www.rfc-editor.org/rfc/rfc7662)): the
endpoint must authenticate the client, and answers `{"active":false}` for an unknown token. A prototype
against Gitea 28.3.0 (`gitea/gitea:28.3.0-rootless`, 2026-11-07) refused it with 401 whatever the
credentials: Gitea's web authentication middleware reads the `token` form field as a user's access token
and rejects the request before the introspection handler runs.

What holds is the token endpoint with an authorization code that cannot exist. Gitea's
`handleAuthorizationCode` ([source, v28.3.0](https://github.com/go-gitea/gitea/blob/v28.3.0/routers/web/auth/oauth2_provider.go))
checks, in this order, the client ID, the client secret, the redirect URI and only then the code. The
prototype got exactly these answers, each a 400 with a JSON body
([RFC 6749 section 5.2](https://www.rfc-editor.org/rfc/rfc6749#section-5.2)):

| Case | `error` | `error_description` | Outcome |
| --- | --- | --- | --- |
| Client ID unknown | `invalid_client` | `cannot load client with client id: ...` | credentials rejected |
| Secret wrong | `unauthorized_client` | `invalid client secret` | credentials rejected |
| Callback not registered | `unauthorized_client` | `unexpected redirect URI` | callback not registered |
| All accepted, code unknown | `unauthorized_client` | `client is not authorized` | passed |

The check sends one request per callback URL (portal and admin, when they differ) through the same
transport sign-in uses: the approved origin, its dial addresses and CA, no proxy, no redirects. It
creates nothing at the provider. A pass needs the exact last answer, so a provider that changes its
wording reports "unexpected answer" with its error code, never a false pass. A failure to connect is
told apart from a certificate the CA does not trust, because the fix differs.

The test changes no connector, flow or session, so it takes no idempotency key and may be repeated. It
is audited (`sso.connector_tested`, with the outcome) because it uses a stored secret against an outside
system. It works on a disabled connector, so a connector can be checked before anyone may use it.

## Choices

- **A list, a drawer per connector and dialogs**, following the interaction system: the drawer shows
  one connector without losing the list; adding is a short form (name, origin, client ID, secret), so a
  dialog.
- **The connector ID is made in the browser**, as the API requires, so the form shows the callback URLs
  to register in Gitea before the client ID is pasted back, with copy buttons.
- **Added connectors start off**, and the dialog offers to test before turning on: a connector nobody
  tested is not offered to people signing in.
- **Turning off is confirmed by typing the name**, with a reason, because the sessions it ends do not
  come back, so undo would promise too much. Replacing credentials warns of the same sign-out in the
  form. Turning on asks for a reason too, as the API requires.
- **A drawer, not yet a detail page.** The interaction system wants a detail page for every list;
  that arrives with UX-127 for every list at once, with an Activity tab.
- **When single sign-on is off in the configuration**, the page still lists what exists and says that
  an operator turns it on, rather than hiding the page.
- **A connector whose origin left the configuration** is marked, since nobody can sign in with it.

Rejected: introspection as the test (refused by Gitea, above); a free-text origin, which the server would
refuse; deleting connectors, which the API does not offer because bindings and sessions refer to them.

## Licences

No new dependency in the product. The live run uses `gitea/gitea:28.3.0-rootless` as a test provider
only ([MIT](https://github.com/go-gitea/gitea/blob/v28.3.0/LICENSE)); it is not shipped.

## Failure modes and tests

- Wrong secret, unknown client ID, unregistered callback, unreachable provider, untrusted certificate,
  unexpected answer: each a distinct outcome; Go tests against a TLS test provider, browser flows on
  mocks, and wrong-secret plus pass against real Gitea in the live run.
- Single sign-on off: the list reads, adding is explained; Go test and browser flow.
- An origin no longer approved: marked in the list, its test says so without a request; Go test.
- A stale revision (`SSO_REVISION_CONFLICT`): explained and the list reloads; browser flow.
- The connector limit and a duplicate ID: explained; browser flow.
- Not a platform administrator, or the portal: the server refuses (existing tests plus a new one for
  the test endpoint); the page shows the denied state.
- Many tests at once: bounded by the provider's exchange slots (`SSO_CAPACITY_EXCEEDED`); Go test.
- A lost keyring: control opens every stored secret at startup and stops with
  `SSO_ENCRYPTED_STATE_INVALID`, and the schema refuses to delete connectors, so it does not start
  until the keyring is restored. Found on lane B's install, whose old keyring was gone. Recovery that
  keeps the platform up is IAM-119; the operator documentation says so meanwhile.
- Gitea's API regenerates the secret on every application update, while its web form does not. The
  check reports that as a refused secret, which is true; found in the live run.

## What done means

From the navigation an administrator sees the connectors, adds one for an approved Gitea with the
callback URLs to register, tests it and sees which part failed or that it passed, turns it on, replaces
its credentials and disables it with a reason, in both languages, with loading, empty, error, denied
and "turned off" states, and someone can then sign in with it.
