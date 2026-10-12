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

Each stage ends with: `templ generate`, `go build ./...`, `go vet ./...`,
`go test ./...` all green, then a merge commit.

| # | Merge | Upstream content | Main work |
|---|---|---|---|
| 1 | `v0.4.0` (`0bbd646`, 21 commits) | Calendar, mail filter fixes, brand kit | Swap calendar to upstream's; renumber its migrations to 107–119; restyle upstream calendar views to Raven's design; keep Raven's logo. |
| 2 | `v0.5.2` (`23a39ae`, 67 commits) | Per-user SQLite storage, managed mode, atomic mail writes | Re-route Raven's own storage additions (push detection, contacts sync throttle, Raven settings) through per-user storage; renumber upstream 105→107 to 119→121. |
| 3 | `v0.6.0` (`5b08c5d`, 54 commits) | Security hardening, mail fixes, phone UI, Express theme | Take security and mail fixes in full, reconciling with Raven's own SSRF guard, sanitizer and iframe CSP (keep the stricter of each). Keep Raven's phone UI; skip Express theme. |
| 4 | — | Ports | Raven calendar extras onto upstream's calendar (iCloud sync-collection and conditional PUT details if not covered, guest suggestions from contacts, invite card in mail). Upstream phone *behaviour* worth keeping in Raven's markup: select all, collapse quoted history, continue draft from thread, show why a message can't load. |
| 5 | — | Verify on the real path | Run the desktop app against a copy of the real database; then alexis after a DB backup. Release. |

## Data safety

- Back up the desktop and alexis databases before any merged build opens them.
- Upstream stage 2 adds an automatic shared→per-user database conversion in
  managed mode. Confirm whether alexis runs in managed mode before deploying.
- Dropping Raven's calendar tables loses only cached events; check stage 1
  that no Raven calendar table holds local-only state (unsent writes, RSVPs).

## Progress

- [ ] Stage 1
- [ ] Stage 2
- [ ] Stage 3
- [ ] Stage 4
- [ ] Stage 5
