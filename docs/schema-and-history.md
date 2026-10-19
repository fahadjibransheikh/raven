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
- New migrations continue from v122 in Raven's own numbering. v123 adds `message_timers`, v124 sets the inline compose default, v125 adds `calendar_reminders`.
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
| 1 | Undo send (5–30 s) + undo archive/trash/move | Built (#32, 2026-10-19) | Undo send holds sends for `undo_send_seconds` (default 10 s). Undo for archive/trash/move already existed. |
| 2 | Snooze until a time | Built (#33, 2026-10-19) | Local only (`message_timers`, schema v123); a Snoozed view; other clients still show the mail. |
| 3 | Follow-up reminders (no reply by a date) | Built (#33, 2026-10-19) | Same timers and worker as snooze. |
| 4 | "Copy code" for sign-in codes in notifications | Built (#31, 2026-10-19) | Code in the notification text, an in-app Copy toast and a chip in the message view. No notification action buttons (Safari, iOS PWA and Tauri lack them). |
| 5 | Tracker stripping (pixels when images load, `utm_` etc. in links) | Built (#29, 2026-10-19) | In the sanitizer: tracking query params and pixel-sized images. |
| 6 | Link mismatch and look-alike sender warnings | Built (#29, #30, 2026-10-19) | Confirm before opening a mismatched link; banner for look-alike senders. No Reply-To check (not stored). |
| 7 | Mail rules (match → move/archive/read/trash) | Build, later | Larger: rules UI plus sync hooks for IMAP, Gmail and Outlook. |
| 8 | Smart tabs + "To Reply" | Build tabs later; defer "To Reply" | Gmail categories can feed tabs; IMAP needs a classifier. "To Reply" leans on AI. |
| – | Reply templates, mute thread, Subscriptions list | Low priority | |
| – | AI summaries/rewrite/suggested replies | Skip | Needs an LLM provider and keys; conflicts with local-first. |

Items 1–6 shipped on 2026-10-19 (plan: `docs/backlog-plan.md`). Next: 7 and 8.

## Calendar issues (reported 2026-10-19)

Reported by the user from the Mac app. All three were built on 2026-10-19.

| # | Issue | Notes |
|---|---|---|
| C1 | Google Meet links don't open | Fixed (2026-10-19). Not Meet-specific: in the desktop app, the shell plugin cancels every `<a target="_blank">` click and hands it to `shell|open`, which the remote page wasn't allowed to call, so Join meeting (Meet and Teams) and invite-card links did nothing. The remote capability now grants `shell:allow-open` (http(s), mailto and tel only). |
| C2 | No reminder before a meeting starts | Built (2026-10-19). The message-timer worker (30 s, single-user and managed) announces timed events starting within `calendar_reminder_minutes` (Off/5/10/15, default 10), once per occurrence (`calendar_reminders`, schema v125). It skips all-day, cancelled and declined events and hidden calendars, and never announces an event that already started. Delivery: `calendar-reminder` SSE event for browser-tab/desktop, Web Push for the rest; clicking opens `/calendar`. Providers' own per-event reminder times are not cached, so the one setting applies to every event. |
| C3 | Agenda view is confusing | Built (2026-10-19). Day groups (Today, Tomorrow, then the date), one compact row per event, repeated all-day events on the same calendar merged into one row with its span, the calendar name shown only when several calendars are in view, past events dimmed and the current and next events marked, and a quiet "Awaiting response" label. "Accepted" is no longer labelled in the agenda. |

## Compose (reported 2026-10-19)

| # | Issue | Notes |
|---|---|---|
| M1 | Compose should open inline by default | Built (2026-10-19). Replies and forwards open below the open conversation (`default_reply_compose_view` = `thread`, the default), and Full width moves the editor into the whole view. New mail keeps opening in the dialog. Migration v124 moved replies left on the old `dialog` default to `thread`. |
