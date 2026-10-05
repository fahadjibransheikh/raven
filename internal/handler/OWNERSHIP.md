# HTTP ownership boundaries

`RegisterRoutes` is wrapped by the application authentication middleware. A
route being registered here does not make it public. The categories below are
the ownership inventory and must be updated when a route is added. Authenticated
routes require an active user; pending or disabled users cannot enter the
handler layer through an application session.

## Public and pre-authentication routes

- Static assets: `GET /assets/*` and `GET /sw.js`.
- Application login: `GET`/`POST /login`, password-MFA continuation under
  `/login/mfa`, recovery-code entry at `/login/mfa/recovery`, restricted factor
  repair under `/login/recovery/*`, plus `GET /auth/google` and
  `GET /auth/google/login/callback`.
- First-run setup: `GET`/`POST routes under `/setup`; each continuation is
  available only while setup is uninitialized and requires the matching
  origin-bound pre-authentication challenge.

Public routes must not call private mail, contact, account, settings, avatar,
or operation repositories. OAuth callback state is a pre-authentication
capability and is not an application session. Password-MFA, recovery repair,
and setup challenges likewise carry no authenticated request user and cannot
access private repositories before full completion.

## Administrator-only operational routes

- Pages under `/admin`, including avatars, contacts, labels, operations, and
  security.
- Security-exception mutations under `/admin/security/*`.
- `GET /api/admin/mail-operations/status`.
- `GET /api/system/processing`.
- Avatar/contact/label diagnostic endpoints and avatar/contact backfill
  mutations registered through `adminRoute`.

These routes may expose aggregate operational state. Administrator status does
not grant access to another user's message, contact, draft, attachment,
signature, outgoing-send, or provider-avatar content.

## Authenticated shared routes

- `GET /api/push/vapid-public-key` returns the instance's public VAPID key.

No authenticated shared route accepts a private resource identifier.

## Authenticated owned routes

The following route groups derive the user from the authenticated request and
constrain every private lookup or mutation to that user:

- Mail pages and lists: `/`, `/email/*`, `/folder/*`, `/mail/folder/*`,
  `/mail/thread/*`, `/search`, `/api/sidebar/*`, and `/api/folders/unread`.
- Folder actions: `POST /api/folders/{id}/read-all` resolves the folder (or a
  unified role) through `ResolveFolderIDForUser` and applies only to folders of
  accounts the caller owns; foreign and missing folders both return 404.
- Message content and actions: `/api/messages/*`, `/api/attachments/*`,
  `/api/inline-content/*`, `/api/remote-content/*`, and
  `/api/remote-assets/*`.
- Contacts: `/contacts*`, `/api/contacts*`, contact import/export, contact sync
  setup/confirmation, provider sync, suppression, and observed-contact cleanup.
- Calendar: `GET /api/calendar/calendars`, `POST /api/calendar/calendars/{id}/selected`,
  `GET /api/calendar/events`, and `POST /api/calendar/sync`. Storage queries
  join `accounts.user_id`, so calendars and events are only visible to their
  owner; `{id}` of a foreign or missing calendar returns 404 with no write, and
  sync only touches the caller's own Google accounts.
- Accounts: account discovery/creation/edit/service/color/test/deletion,
  account contact settings, signatures, and `/api/mail/sync*`.
- Sending addresses: `/api/accounts/{id}/identities*` (list, add, delete,
  set default, refresh from Gmail, dismiss suggestion) require an owned
  account before any write or provider call; storage re-checks ownership in
  SQL and foreign/missing accounts and identities both return 404.
- Account OAuth: `/api/accounts/oauth2/authorize`,
  `/auth/google/mailbox/callback`, and `/auth/microsoft/mailbox/callback` use a
  single-use flow bound to the current user, session, and provider.
- Settings, signatures, UI preferences, and Web Push subscriptions.
- Compose, staged compose attachments, drafts, outgoing sends, and mail
  operations.
- `GET /api/events`, whose EventBus messages require explicit account, user,
  multi-user, or administrator scope.
- `GET /api/avatars/{hash}` and `POST /api/avatars/warmup`, which require the
  sender email to be visible through the current user's messages or contacts.
- `GET /api/provider-avatar`, which requires an exact provider URL stored on
  the current user's profile or contact before any outbound request.

Foreign and nonexistent private identifiers must be indistinguishable. Unless
a route explicitly documents another privacy-preserving result (account
deletion status does), both return `404`. Ownership is checked before database
mutation, queue insertion, blob access, translation, remote fetch, or provider
traffic. The same rule applies to administrators.
