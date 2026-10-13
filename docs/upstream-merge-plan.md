# Upstream (gofer) merge plan

Started 2026-10-12. Branch: `upstream-merge`. Goal: bring in gofer's work since the
2026-09-17 merge-base (`338f103`) — calendar, per-user SQLite storage, security
hardening, mail fixes, and the good parts of the phone UI — without changing
Raven's design.

## Decisions

- **Stay a tracking fork.** Keep the `upstream` remote and the
  `github.com/cristianadrielbraun/gofer` module path; do not detach the GitHub
  fork. Renaming the module would make every future merge conflict on imports.
- **Real merges, staged at upstream releases.** Upstream history is linear, so
  each stage is `git merge <upstream tag>`. Merge commits record ancestry, so
  later merges only carry new upstream work.
- **Raven's design wins in views.** For `internal/views`, `assets/css` and
  layout JS, keep Raven's markup and styling; port upstream *behaviour*
  (handlers, data, security logic) into Raven's markup. Never hand-edit
  `*_templ.go` — resolve the `.templ` and run `templ generate`.
- **Upstream's calendar replaces Raven's.** Both sides built a calendar with
  incompatible schemas under the same table name (`calendar_events`). Upstream's
  is the larger one (sources, recurrence, series edits, invitations, Teams/Meet,
  CalDAV). Calendar data is a provider cache, so a migration drops Raven's
  calendar tables and the calendar re-syncs. Raven-only calendar features are
  ported onto upstream's afterwards (stage 4).
- **Schema renumbering.** Both sides used schema versions 94–106 for different
  migrations. Every existing Raven database is at Raven's v106, so Raven's
  chain 1–106 stays as is. New chain on top:
  - v106→v107: drop Raven calendar tables (`calendar_events`, `calendars`,
    `calendar_account_state`, and any Raven-only calendar invite tables).
  - Upstream's 93→94 … 106→107 run as 107→108 … 120→121, in order.
  - `schema.sql` (fresh installs) must end in the same state as the chain;
    a test compares the two.

## Stages

Each stage ends with: `templ generate`, `tailwindcss` build, `go build ./...`, `go vet ./...`,
`go test ./...` all green, then a merge commit.

| # | Merge | Upstream content | Main work |
|---|---|---|---|
| 1 | `v0.4.0` (`0bbd646`, 21 commits) | Calendar, mail filter fixes, brand kit | Swap calendar to upstream's; renumber its migrations to 107–119; restyle upstream calendar views to Raven's design; keep Raven's logo. |
| 2 | `v0.5.2` (`23a39ae`, 67 commits) | Per-user SQLite storage, managed mode, atomic mail writes | Re-route Raven's own storage additions (push detection, contacts sync throttle, Raven settings) through per-user storage; renumber upstream 105→107 to 119→121. |
| 3 | `v0.6.0` (`5b08c5d`, 54 commits) | Security hardening, mail fixes, phone UI, Express theme | Take security and mail fixes in full, reconciling with Raven's own SSRF guard, sanitizer and iframe CSP (keep the stricter of each). Keep Raven's phone UI; skip Express theme. |
| 4 | — | Ports | **Must-fix before release:** after v107, existing accounts have no calendar sources until each account's calendars are discovered and chosen again (and iCloud CalDAV settings re-entered). Carry Raven's calendar selection over automatically (run discovery for accounts that had Raven calendars and mark the previously selected ones), then verify on a copy of the real DB with logins intact. Raven calendar extras onto upstream's calendar (iCloud sync-collection and conditional PUT details if not covered, guest suggestions from contacts, invite card in mail). Upstream phone *behaviour* worth keeping in Raven's markup: select all, collapse quoted history, continue draft from thread, show why a message can't load. |
| 5 | — | Verify on the real path | Run the desktop app against a copy of the real database; then alexis after a DB backup. Release. |

## Data safety

- Back up the desktop and alexis databases before any merged build opens them.
- Upstream stage 2 adds an automatic shared→per-user database conversion in
  managed mode. Confirm whether alexis runs in managed mode before deploying.
- Dropping Raven's calendar tables loses only cached events; check stage 1
  that no Raven calendar table holds local-only state (unsent writes, RSVPs).

## Progress

- [x] Stage 1 (2026-10-12). Notes:
  - Removed Raven's calendar handlers, models, invite card (`email_invite.go`,
    `invite.templ`, `invite-card.js`, `internal/mail/ical`), `calendar-app.js`
    and its CSS, and the `scripts/render-calendar` / `render-invite` helpers.
  - Google: Raven still requests `calendar.calendarlist.readonly` +
    `calendar.events`; `GetGoogleCalendarTokenForAccount` accepts that pair, so
    existing Raven grants keep working without a reconnect.
  - Microsoft: kept Raven's typed `ErrMicrosoftCalendarConsentRequired`.
  - `migrateV109ToV110` (upstream CalDAV credentials) skips existing columns.
  - Calendar page uses Raven's head, scripts, sidebar surface and no serif;
    the rest of upstream's calendar markup is unrestyled (stage 4).
  - Dropped upstream's per-section settings headers (Raven's single header).
- [x] Stage 2 (2026-10-12). Notes:
  - Desktop and alexis run personal/open mode, so the per-user conversion
    never runs for them. Managed mode lacks these Raven routes (no per-user
    port yet): account identities and label, message unsubscribe, folder
    read-all/empty, `/hooks/gmail` and `/hooks/graph`, provider push
    (`StartPush`).
  - `renderMailboxView` returns 404 only for missing rows/foreign accounts;
    other storage errors are now 503 (Raven used to ignore some).
  - `raven_calendar_carryover` (v107) keeps each account's Raven calendar list
    and selection for stage 4.
  - Per-user conversion (managed mode only) maps upstream shared versions
    105/106/107 to Raven 119/120/121 and inventories Raven-only tables.
  - Known upstream test issue: 3 `TestUserStorageMigration*` tests fail when
    TMPDIR is a symlink (macOS `/var` -> `/private/var`); they also fail on
    pristine upstream v0.5.2. Run storage tests with
    `TMPDIR=/private/tmp/<dir>` on macOS.
- [x] Calendar carryover (2026-10-12): first discovery per account applies the
  saved Raven selection (CalDAV URLs matched loosely); the calendar worker runs
  discovery for waiting accounts at startup and every 10 minutes until it
  succeeds (iCloud via CalDAV autodiscovery with the mailbox login).
  **Not yet verified against real providers** - check on the desktop app with
  real accounts before release.
- [x] Stage 3 (2026-10-13). Notes:
  - Security merged to the stricter of both sides on every axis: one
    `netguard` with upstream's API and admin-approval exceptions plus Raven's
    blanket NAT64/6to4 block and redirect checks; Raven's allow-list sanitizer
    with upstream's API and attack tests; upstream's signed message-file
    links and image relay (iframe CSP `img-src 'self' data:`); one
    `serveMailFile` that judges type by bytes; iframe sandbox without popups.
  - Page-wide CSP is now `script-src 'self'`: ~120 inline handlers became
    `data-on-*` hooks (actions.js), inline scripts moved to settings-page.js,
    saved-panel-size.js; htmx `allowEval=false`.
  - Private (LAN) CardDAV/CalDAV: restored for open/personal mode. The
    typed or saved server (exact host:port, the "anchor") works without an
    exception; hosts learned from responses stay public-only. Managed mode
    still needs an admin "Mail security" exception.
  - Deferred upstream UI, ported 2026-10-13 in Raven's style: select-all menu
    and whole-list banner, wizard "test before saving", settings card
    structure, reply buttons on every thread message header, quote toggle
    styling, Expand/Collapse all. Per-message thread sheets skipped (would
    replace Raven's reader layout). Calendar restyled for desktop and phone. Express theme assets
    are in the tree but unused.
  - Edit-account wizard layout (pre-existing): its CSS lived in the lazily
    loaded Add Account dialog, so Edit opened unstyled. Fixed 2026-10-13 by
    moving the rules into input.css; phone layout ported from upstream.
  - 2026-10-13 owner decisions: port the deferred upstream UI in Raven's
    style; restyle the calendar; fix the edit wizard (done).
  - History cleanup pending owner approval: merge commit a413724 swept 482 MB
    of local tmp/ build output into history (untracked since 22c11d8, but the
    blobs remain). Rewrite before any push: rebuild that one merge commit
    without tmp/ and replay the commits after it; keep upstream hashes.
  - **Verify on the desktop app**: Tauri-injected scripts under the new
    `script-src 'self'` policy (dock badge, notifications, updater).
- [ ] Stage 4 (needs decisions, see Handoff)
- [ ] Stage 5 (partly done, see Handoff)

## Handoff (2026-10-13)

Branch `upstream-merge` (not pushed): level with upstream gofer `5b08c5d`
(v0.6.0). All Go tests (`task test`, which now resolves TMPDIR on macOS), JS
tests (157/157), vet and the Tailwind build pass.

Verified without credentials:
- Desktop data copy and alexis data copy (alexis: personal mode, at `4ca1a1b`)
  both migrate v106 -> v121; schema identical to a fresh install; messages
  intact; integrity ok; carry-over keeps 14 calendars (8 selected) each.
- Smoke runs of the embedded release build on an offline copy: mail, reader,
  calendar, contacts, settings, search, add/edit account dialogs render in
  Raven's design; no script-policy violations logged.
- Signed message-file links: valid -> 200 with sandbox CSP and nosniff;
  tampered signature, retargeted URL, expired link, other message -> 404.
- `/hooks/gmail` and `/hooks/graph` still registered and exempt from sign-in.

Needs Fahad (real accounts / devices):
1. Desktop app from this branch against a **backup** of the real data dir:
   - watch the log for `calendar carryover: ... restored Raven selection` for
     all four accounts, then check 8 calendars are selected;
   - Google: discovery should work without reconnecting (Raven's narrower
     `calendarlist.readonly` + `calendar.events` grant is accepted);
   - Outlook: the calendar token refresh now asks for `Calendars.Read`
     (upstream) where Raven asked for `Calendars.ReadWrite`; if discovery
     logs a consent error, point `GetMicrosoftGraphCalendarTokenForAccount`
     at the write scope;
   - iCloud: CalDAV autodiscovery with the mailbox app password;
   - Tauri-injected scripts (dock badge, status, compose shortcut) under the
     new `script-src 'self'` page policy.
2. Decisions: which deferred upstream UI to port in Raven's style (stage 3
   notes); whether to restyle the calendar page beyond the shell; the
   pre-existing edit-account wizard layout. (Private-LAN DAV in personal
   mode: decided, anchor works without an exception.)
3. Release path: merge to `master` -> `release` skill -> on alexis back up
   `~/raven-server/data` (gofer.db, accounts/, secret.key, VAPID key) before
   `update.sh`. First start migrates 106 -> 121 and runs calendar carry-over.
   alexis already runs the merge branch (deployed by bundle, 2026-10-13; data
   backed up to `~/raven-server/backup-0.3.3-2026-10-13-data`). Its
   `update.sh` does `git pull --ff-only` on the checked-out branch, which has
   no upstream, so it fails until switched back: after the merge is pushed,
   `git -C ~/raven-server/src checkout master && git -C ~/raven-server/src pull --ff-only`,
   then `update.sh`, and confirm `git log -1` matches the release.

Known leftovers: Express theme fonts/icons (~570 KB) are embedded but unused
(left to avoid a merge-conflict spot).

## Post-merge feature backlog (added 2026-10-12)

Source: a comparison with Bytemail's marketing page (unverified), against the
`upstream-merge` branch. Checked upstream gofer `master` (5b08c5d) and its only
other branch (`storage/per-user-sqlite`, already merged here) on 2026-10-12:
none of these exist upstream and no upstream issue proposes them. Decision for
all: **build in Raven**, after stage 5, in Raven-owned files (new
`raven_*`-prefixed migrations/tables, separate handler files) to keep future
upstream merges small. Re-check upstream before starting each one.

| # | Feature | Decision | Notes |
|---|---|---|---|
| 1 | Undo send (5–30 s) + undo archive/trash/move | Build | Reuse the scheduled-send path: delayed send + cancel toast. |
| 2 | Snooze until a time | Build | Shares a timer/wake-up queue with 1 and 3. |
| 3 | Follow-up reminders (no reply by a date) | Build | Same queue; build 1–3 as one feature. |
| 4 | "Copy code" for sign-in codes in notifications | Build | Small; Web Push + desktop notification action. |
| 5 | Tracker stripping (pixels when images load, `utm_` etc. in links) | Build | Hooks into Raven's existing sanitizer/image proxy; check stage 3's upstream image proxy first. |
| 6 | Link mismatch and look-alike sender warnings | Build | Local only; moderate. |
| 7 | Mail rules (match → move/archive/read/trash) | Build, later | Larger: rules UI plus sync hooks for IMAP, Gmail and Outlook. |
| 8 | Smart tabs + "To Reply" | Build tabs later; defer "To Reply" | Gmail categories can feed tabs; IMAP needs a classifier. "To Reply" leans on AI. |
| – | Reply templates, mute thread, Subscriptions list | Low priority | |
| – | AI summaries/rewrite/suggested replies | Skip | Needs an LLM provider and keys; conflicts with local-first. |

Order: 1–3 together, then 4 and 5, then 6, then 7 and 8.

## Decision 2026-10-13: stop tracking upstream after this release

The owner decided Raven stops merging gofer after the v0.6.0 merge is released.
After the release:
- Rename the Go module away from `github.com/cristianadrielbraun/gofer`.
- Detach the GitHub fork (owner does this in GitHub settings).
- Keep the `upstream` remote read-only, as a reference only.
- Port wanted upstream changes by hand, with a test each. Security fixes
  always, mail fixes usually, features on request. Review upstream's log
  about every two weeks.
- New schema migrations follow Raven's own numbering from v122.
- The README presents Raven as its own project. LICENSE keeps both MIT
  copyright notices, as the license requires.
- Possible later cleanup: remove unused managed-mode / per-user storage code.
- `GOFER_*` environment variable names stay for now (existing `.env` files
  depend on them); renaming them needs a compatibility shim.
