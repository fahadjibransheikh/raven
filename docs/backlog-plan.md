# Feature backlog 1–6: plan

Status: done, 2026-10-19 (PRs #28–#33 merged). Scope chosen by the user: backlog items 1–6 from
`docs/schema-and-history.md`. Mail rules (7) and smart tabs (8) are left for later.

One branch and one PR per feature group, each from `main`. No merges and no deploys
to the Oracle VM without asking. Every PR needs `go test ./...` (with the Taskfile
`TMPDIR` fix) and `pnpm test` to pass, plus new tests that fail without the change.

## Already built

Undo for archive, trash, move and spam works end to end. The server returns `moved`
(`internal/handler/message_mutations.go:63-71`), and the client shows an undo toast
(`assets/js/app.js` `showUndoToast`). Item 1 is reduced to undo send.

## PR 1: Undo send (`undo-send`)

- Setting `undo_send_seconds` in the UI settings. Values are 0/5/10/20/30, default 10.
  Clamp on the server: `MergeUISettings` has no allowlist.
- `/compose` queues with `sendAfter = now + delay` and `is_scheduled = false`. A timer
  (`time.AfterFunc`) calls `signalOutgoingWorker` at `sendAfter`, so the send doesn't
  wait for the 30 s ticker. The ticker stays as the fallback.
- Undo uses the existing `POST /api/outgoing-sends/{id}/cancel`. `CancelOutgoingSend`
  only cancels rows that are pending, failed or ambiguous. Once the worker has claimed
  the row, the toast says it was already sent.
- Client: hide the compose dialog without resetting it. Show a "Sending… Undo" toast
  for the delay. Undo cancels the send and reopens the same form. The form is reset
  only on "sent", which the code already does.
- Check that a canceled send writes no Sent copy. Sent copies are written only after
  delivery, in their own loop (`c3eb64d7`).
- Done when:
  - a Go test shows that `/compose` with a delay sets `send_after` to about now + delay;
  - a Go test shows that cancel inside the window sets the row to canceled and the
    worker skips it;
  - a JS test shows that Undo posts cancel and restores the compose form.

## PR 2: Tracker stripping + link mismatch (`link-safety`)

- Sanitizer (`internal/mail/message/sanitize.go`):
  - drop tracking parameters from `href`: `utm_*`, `fbclid`, `gclid`, `dclid`,
    `msclkid`, `mc_eid`, `mc_cid`, `mkt_tok`, `_hsenc`, `_hsmi`, `yclid`;
  - leave `mailto:` and List-Unsubscribe untouched;
  - drop images that are 1×1, zero-size or hidden. This is a heuristic with no list of
    tracker hosts (`shortcut:`).
- Link mismatch: when an anchor's text parses as a URL or host whose registrable
  domain differs from the href's, add `data-link-mismatch="<href host>"`. The frame's
  link shim passes it to the parent, which asks `ravenConfirm` before opening the link.
- Check whether sanitized HTML is cached. If it is, old bodies keep their trackers
  until the cache is rebuilt. Say so, and don't bump the cache just for this.
- Done when sanitizer table tests cover stripped params, kept params, pixels and
  mismatch, and a JS test shows the confirm appears on a mismatched link click.

## PR 3: Look-alike sender warning (`sender-warning`)

Local checks only: no SPF or DKIM.

- The display name contains an email address that differs from the From address.
- The From domain is punycode (`xn--`) or has non-ASCII characters.
- The From domain is within edit distance 1 of a domain in the user's observed
  contacts, but is not that domain.
- The Reply-To domain differs from the From domain, and the From domain is in the
  user's contacts.

Show the warning in a banner that reuses the remote-content banner in
`internal/views/mailview.templ`. Done when unit tests cover each check, including
known domains that must not be flagged, and a view test shows the banner.

## PR 4: Snooze + follow-up reminders (`snooze`)

This is local-only. Gmail and Graph have no snooze API, so other clients still show
snoozed mail.

- Migration v122 → v123 adds a new table, `message_timers`:
  - columns: `id`, `account_id`, `message_id`, `thread_key`, `kind`
    (snooze|followup), `due_at`, `created_at`;
  - it is reversible because it only adds a table.
- Snoozed threads leave the list: both the page query and the count, in unified views
  too. A "Snoozed" view lists them.
- One due loop on a 30 s ticker, like `calendar_worker.go`.
  - Snooze due: delete the timer, mark the thread unread through the normal mutation
    path, and publish a new-mail event.
  - Follow-up due: if the thread has no message newer than `created_at` from an
    address other than the user's own, notify and mark unread. Otherwise drop the
    timer.
- A new inbound message in a snoozed thread clears the snooze.
- Resurfaced threads keep their date order (`shortcut:` no move to the top).
- UI: Snooze and "Remind me" menus with presets (later today, tomorrow, next week,
  custom) on the message toolbar and the list row actions. Keyboard shortcut `b`
  snoozes.
- Highest risk: list positions drift if the filter misses the count or any query
  path. Stop and re-plan if the virtual list shows gaps.

## PR 5: Copy sign-in codes (`copy-code`)

Notification action buttons are not built: Safari, the iOS PWA and Tauri notify
don't support them.

- At `publishNewMail`, extract a code from the latest message: 4–8 digits or
  alphanumerics next to code/verification/OTP/passcode/PIN, in the subject or body
  text. IMAP snippets are only the subject, so read the stored text body when there
  is one. Never log the code.
- Put the code first in the notification text ("Code 123456 · sender").
- When the app is open, show an in-app toast with a Copy button.
- Show a "Copy code" chip in the message view.
- Done when table tests cover extraction, including false positives (prices, dates,
  order numbers), and a JS test covers the toast copy.

## Order

PR 1 → PR 2 → PR 3 → PR 4 → PR 5. PR 2, PR 3 and PR 5 touch separate code from PR 1
and PR 4 and can run in parallel. Mark each item done in the backlog table when its PR
merges.
