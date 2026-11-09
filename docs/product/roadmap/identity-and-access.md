# Identity and access

Accounts, enterprise sign-in, sessions, roles and permissions for people, services and
agents. Every check happens on the server.

Reviewed 2026-11-09 against the [Keycloak administration guide](https://www.keycloak.org/docs/latest/server_admin/index.html),
[GitLab self-managed authentication](https://docs.gitlab.com/user/profile/account/two_factor_authentication/),
[NIST SP 800-63B-4](https://pages.nist.gov/800-63-4/sp800-63b.html),
[Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/),
[OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html), and
[SCIM RFC 7644](https://www.rfc-editor.org/rfc/rfc7644.html). The standards-based
federation, provisioning, and scoped access items still fit the deployment model.
Add phishing-resistant local authentication as IAM-206 so operators can use
security keys without depending on an upstream identity provider.

- Owns: `services/control/internal/modules/identity`, `sso`, `workspaces`.
- Depends on: architecture-and-scale.

## P1 Enterprise release

- [x] IAM-101 Local accounts: invitation, login, logout, password reset, lockout (P1) - Catalog: F001 - Evidence: dcac35a5 and [disconnected account acceptance](../../research/local-accounts.md#verification)
- [ ] IAM-102 Generic OIDC sign-in (replaces the Gitea-only connector) (P1) - Catalog: F445
- [ ] IAM-103 LDAP and Active Directory directory login and group sync (P1) - Catalog: F447
- [ ] IAM-104 SAML 2.0 sign-in (P1) - Catalog: F446
- [ ] IAM-105 MFA with TOTP and recovery codes; step-up for sensitive actions (P1) - Catalog: F434, F456
- [ ] IAM-106 ownsolid as an OIDC provider for catalog apps such as DeepParser (P1)
- [ ] IAM-107 Roles: platform admin, tenant admin, workspace admin, developer, operator, member, viewer, auditor; custom roles (P1) - Catalog: F479
- [ ] IAM-108 Resource-level permissions with inheritance and an explain view (P1) - Catalog: F480, F487
- [ ] IAM-109 Service accounts and scoped API keys with expiry, rotation and revocation (P1) - Catalog: F464, F484
- [ ] IAM-110 Group-to-role mapping from upstream identity with preview (P1) - Catalog: F453
- [ ] IAM-111 Session management: idle and absolute timeout, device list, forced sign-out, revocation propagation (P1) - Catalog: F454, F571
- [x] IAM-112 Separate Admin and Portal entrances with audience-bound sessions (P1) - Catalog: F424, F432, F545 - Evidence: a2531e60 and [disconnected browser entrance acceptance](../../research/browser-entrance-sessions.md#verification)
- [ ] IAM-113 Enable single sign-on from the typed install configuration: `ownsolid-install` has no input for providers, their CA or the keyring, so an installed product cannot turn it on (P1)
- [ ] IAM-114 Refuse two browser entrances on one hostname unless the deployment opts in, because browsers do not isolate cookies by port; the opt-in, the sign-in page and the operator documentation state that the audience check is then the only separation ([ADR 0027](../../decisions/0027-entrance-addresses.md)) (P1)
- [x] IAM-115 Users page: accounts with status and filters, invite and resend, disable and enable an account with a reason (P1) - Evidence: 24d6b00; real Chromium on an offline install on `ownsolid-cpu` with a TLS mail server accepted a real invitation, disabled the new account, which ended its session, and enabled it, three runs in a row, 2026-11-07
- [x] IAM-116 Workspaces page: list, create and rename workspaces and manage who belongs to each (P1) - Evidence: 3a406f9; real Chromium on an offline install on `ownsolid-cpu` created a workspace, added a real account, changed its role, removed it, was refused for the last owner and disabled the workspace with its reason in the audit trail, three runs in a row, 2026-11-07
- [x] IAM-117 Single sign-on page: list, add, test and disable connectors, on the existing connector API (P1) - Evidence: c23e230; real Chromium on an offline install on `ownsolid-cpu` with a real Gitea 28.1.0 over TLS added a connector, was told of a wrong secret and an unregistered callback, passed the check, turned it on, linked an account, signed in through Gitea, and turning it off from another browser ended that session, three runs in a row, 2026-11-07. Single sign-on was turned on by hand under `control_settings` (IAM-113)
- [ ] IAM-118 Test a connector with a real sign-in that shows the identity returned and signs nobody in, as Auth0 and Cloudflare One do; the connection check of IAM-117 stops short of the provider's login (P1)
- [ ] IAM-119 Survive a lost single sign-on keyring: control starts, password sign-in works, unreadable connectors are marked and their credentials can be replaced; today a keyring that cannot open a stored secret stops control at startup and connectors cannot be deleted (P1)

## P2 Fleet operations

- [ ] IAM-201 SCIM 2.0 provisioning (P2) - Catalog: F450
- [ ] IAM-202 Attribute and relationship-based policies with versioned rules and dry run (P2) - Catalog: F481, F573
- [ ] IAM-203 Approval workflows and two-person review for high-risk actions (P2) - Catalog: F430, F440
- [ ] IAM-204 Just-in-time access and break-glass accounts with automatic expiry (P2) - Catalog: F441, F455
- [ ] IAM-205 Agent identity: on-behalf-of tokens with the user as subject and the agent as actor (P2) - Catalog: F572, F485
- [ ] IAM-206 Phishing-resistant WebAuthn security keys for local sign-in and step-up, with offline enrollment, revocation and recovery and separate Admin and Portal origin validation (P2)

## P3 Multi-site

- [ ] IAM-301 Global identity across sites with local fallback when the WAN is down (P3)

## P4 Breadth

- [ ] IAM-401 Kerberos, CAS and WS-Federation (P4) - Catalog: F448, F449

Vendor identity products (Entra ID, Okta, Keycloak, Feishu, DingTalk and others) are in
the compatibility matrix. Catalog rows absorbed: F001, F005 to F008, F424, F432 to F434,
F444 to F465, F478 to F488, F571 to F573, F575.
