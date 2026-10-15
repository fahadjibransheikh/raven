# Schema numbering, compatibility and backlog

Raven began as a fork of [gofer](https://github.com/cristianadrielbraun/gofer) and
merged it up to v0.6.0 (`5b08c5d`) in October 2026. It split on 2026-10-15 and no
longer merges from gofer. This file keeps what the code still depends on from that
history.

## Schema numbering

- Raven's own chain is v1–v106. Every database made before the merge is at v106.
- v107 drops Raven's old calendar tables (a provider cache that re-syncs) and adds
  `raven_calendar_carryover`, which keeps each account's previous calendar list and
  selection so the first discovery can reselect them.
- gofer's migrations 93→94 … 106→107 run as Raven's 107→108 … 120→121, in order.
  The managed-mode per-user conversion maps gofer's shared versions 105/106/107 to
  Raven's 119/120/121.
- v122, together with storage layout step 2, renames the old `gofer_*` tables.
- New migrations continue from v122 in Raven's own numbering.
- `schema.sql` (fresh installs) must end in the same state as the migration chain; a
  test compares the two.

## Compatibility with installs from before the rename

Kept on purpose; don't remove without a migration path:

- `RAVEN_*` environment variables fall back to `GOFER_*` (`internal/legacyenv`), and
  Raven logs a notice when it reads one.
- `gofer.db` is renamed to `raven.db` on first start.
- `gofer_*` cookies and browser-storage keys move to the new names.
- Key-derivation labels and synthetic Message-ID domains still use the old names,
  because existing data depends on them.
- `LICENSE` keeps both MIT copyright notices, as the license requires.

## Known limitations (as of 2026-10-13)

- Managed mode lacks these routes, which have no per-user storage port yet: account
  identities and label, message unsubscribe, folder read-all/empty, `/hooks/gmail`
  and `/hooks/graph`, and provider push (`StartPush`). The desktop app and the
  server run in open/personal mode, so they're unaffected.
- On macOS, some storage tests fail when `TMPDIR` is a symlink (`/var` →
  `/private/var`). Run tests with `task test`, which resolves it.
- The Express theme (`assets/css/themes/express.css`, `assets/icons/express/`,
  ~540 KB) is in the tree but unused.

## Feature backlog (2026-10-12)

From a comparison with Bytemail's marketing page (unverified). Build in Raven.

| # | Feature | Decision | Notes |
|---|---|---|---|
| 1 | Undo send (5–30 s) + undo archive/trash/move | Build | Reuse the scheduled-send path: delayed send + cancel toast. |
| 2 | Snooze until a time | Build | Shares a timer/wake-up queue with 1 and 3. |
| 3 | Follow-up reminders (no reply by a date) | Build | Same queue; build 1–3 as one feature. |
| 4 | "Copy code" for sign-in codes in notifications | Build | Small; Web Push + desktop notification action. |
| 5 | Tracker stripping (pixels when images load, `utm_` etc. in links) | Build | Hooks into the sanitizer and image relay. |
| 6 | Link mismatch and look-alike sender warnings | Build | Local only; moderate. |
| 7 | Mail rules (match → move/archive/read/trash) | Build, later | Larger: rules UI plus sync hooks for IMAP, Gmail and Outlook. |
| 8 | Smart tabs + "To Reply" | Build tabs later; defer "To Reply" | Gmail categories can feed tabs; IMAP needs a classifier. "To Reply" leans on AI. |
| – | Reply templates, mute thread, Subscriptions list | Low priority | |
| – | AI summaries/rewrite/suggested replies | Skip | Needs an LLM provider and keys; conflicts with local-first. |

Order: 1–3 together, then 4 and 5, then 6, then 7 and 8.
