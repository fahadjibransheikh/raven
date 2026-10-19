---
target: Mail surface
total_score: 27
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
target_identity: "file:/Users/fahad/code/raven/internal/views/maillist.templ"
target_fingerprint: "sha256:f24917c6311f7981ac13540124d3c9c59f8f09f6eed6d5d4a48fc4f21cfc2608"
target_path: /Users/fahad/code/raven/internal/views/maillist.templ
timestamp: 2026-10-19T12-11-35Z
slug: internal-views-maillist-templ
---
Method: dual-agent (A: design review · B: detector + browser)
Target: Mail surface (internal/views/maillist.templ, plus mailview.templ and sidebar.templ)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2 | Unread is colour-only in card rows; the header pill "19" vs the sidebar "1" is ambiguous; there is no sync or last-updated cue |
| 2 | Match System / Real World | 3 | Plain copy overall; "Hide all others / Show and hide others" is jargon; a bare "Unread" text toggle reads as a label |
| 3 | User Control and Freedom | 3 | Escape, Cancel and Clear all exist; delete sits one small icon from Archive |
| 4 | Consistency and Standards | 3 | Unread is semibold in table view but colour-only in cards; reader actions are duplicated in the toolbar and per message |
| 5 | Error Prevention | 3 | Row hover actions are 24px targets with a 2px gap, and archive and delete are adjacent |
| 6 | Recognition Rather Than Recall | 2 | The list toolbar is about 8 unlabelled icons; search operators exist only in a tooltip |
| 7 | Flexibility and Efficiency | 4 | Command palette, R/F/E key hints, resizable panes, custom card layout, select-all across a folder |
| 8 | Aesthetic and Minimalist Design | 3 | Calm surface; the list toolbar and reader header add noise; reserved empty icon slots waste row width |
| 9 | Error Recovery | 2 | Generic empty state; no failure or offline surface seen |
| 10 | Help and Documentation | 2 | Search and filter power is discoverable only through the Filters panel and a tooltip |
| **Total** | | **27/40** | **Acceptable (top of band)** |

## Design Specificity Verdict

**LLM assessment:** about 60% authored and 40% category default. Raven's own touches:
- Paper cards on a warm canvas, separated by tone.
- Mono tabular dates.
- The 2px account bar at the start of each row.
- The ink Reply exception.
- Rationed magenta.

The boxed-icon empty state, pill counts, icon-only toolbars and sidebar are shared shadcn/Linear grammar. The biggest gap: the product's wedge, local-first with your data on your machine, has no visible expression on the Mail surface. There is no sync state, no "stored on this device" cue, and no remote-content state in the reader. Without the logo it could be any well-made mail client.

**Deterministic scan:** exit 0, 12 advisories, all `design-system-font-size`, and no built-in anti-pattern rules fired.
- `mailview.templ:246` and `:952` (22px): a gap in DESIGN.md's front matter, now fixed with a `subject` token.
- 13px (`maillist.templ:851`, `:1011`; `sidebar.templ:697`, `:729`, `:760`, `:936`, `:973`, `:1030`): real drift from the Two-Size Rule. 13px is the second most common size live (66 text nodes against 69 at 14px).
- 9px avatar fallback (`maillist.templ:1191`) and 10px `<kbd>` (`sidebar.templ:1022`): real, but small.
- Directory mode (`detect internal/views`) silently scans no `.templ` files. Only file-targeted runs work.

**Visual overlays:** none. The live CSP (`script-src 'self'`) blocked both an inline script and `localhost:8400/detect.js` (securitypolicyviolation: `script-src-elem`). The live server was stopped. Fallback measurements:
- All measured text passes 4.5:1. The lowest is 5.93:1, on preview, date and account text in dark mode.
- Rows are 68px, matching DESIGN.md.
- Card rows can't be reached with Tab: there is no tabindex or role. Arrow and j/k keys were not tested.

## Overall Impression

A calm, competent, efficient mail client whose power-user features are genuinely good. Its two weak spots are state legibility (what is unread, what is synced, who a sent message went to) and the expression of the local-first idea. The single biggest opportunity is to make the row and list header carry state clearly, with weight and labels instead of colour and icons.

## What's Working

- **The row grammar:** mono tabular dates, the account bar and the unread dot make a scannable row that belongs to this product (`maillist.templ` ~975–1006).
- **Efficiency taught in place:** the reply bar shows R, F, E and ⌘K hints, and the command palette and custom card layout serve power users without cluttering the defaults.
- **Accent discipline:** magenta stays on Send, the unread dot and the active bar. The ink Reply is a deliberate, well-judged exception.

## Priority Issues

**[P1] Unread is colour-only in card rows, and invisible to screen readers.**
- *Why:* Alex can't scan for unread mail, and Sam can't tell unread from read at all. Live rows compute `font-weight: 400` whether read or unread, and the dot has no text alternative.
- *Fix:* add `font-semibold` to sender and subject when `!email.IsRead` in card rows, as the table view already does at `maillist.templ:1108`. Add `<span class="sr-only">Unread, </span>` or an aria-label on the row link. Make sure the optimistic-read override (`input.css:1467`) still strips the weight.
- *Suggested command:* /impeccable typeset, then /impeccable audit

**[P1] The phone Compose button can't fire.**
- *Why:* `maillist.templ:57` uses `onclick="openNewCompose()"`. The live CSP is `script-src 'self'` with no inline allowance, and `actions.js` has no `onclick` shim, so the browser blocks the handler. This is the primary action on the phone/PWA layout. I didn't tap it on a real phone.
- *Fix:* use `data-on-click='["openNewCompose"]'`, as the sidebar button does at `sidebar.templ:331`.
- *Suggested command:* /impeccable harden

**[P1] Sent and Drafts rows show you as the sender.**
- *Why:* every visible Sent row reads "Fahad Sheikh", so "what did I send to X?" can't be answered from the list.
- *Fix:* in the sent and drafts roles, make line 1 the recipient with a Pencil "To" prefix. The markup already exists around `maillist.templ:1017`.
- *Suggested command:* /impeccable clarify

**[P2] Table view is unreadable at the default list width.**
- *Why:* at about 500px the Subject column is squeezed to "Re: W", with horizontal scroll and 20 identical account dots. The Outlook-density promise fails here.
- *Fix:*
  - Give Subject `flex: 1` with a minimum width of about 40%.
  - Merge From and To into one column chosen by folder role.
  - Hide the account column when only one account is visible.
  - Widen the pane when switching to table view.
- *Suggested command:* /impeccable layout

**[P2] The list header is a wall of controls, and its count is ambiguous.**
- *Why:* about 10 controls sit across two rows (well over 4 options at one decision point). The "19" pill doesn't match the sidebar's "1", and Sent shows "1998".
- *Fix:*
  - Fold sort, layout and columns into one View menu, keeping checkbox, view toggle and Unread visible.
  - Label the count ("19 messages" or "1 unread") to match the sidebar's meaning.
- *Suggested command:* /impeccable distill

## Persona Red Flags

**Alex (power user):**
- Unread rows have no weight.
- Hover actions at `absolute right-2 top-1` cover the date and dot being triaged.
- The Sent list can't be scanned by recipient.
- The table view is clipped.
- The list header has no key hints (only the reader shows them).

**Sam (screen reader, keyboard):**
- Card rows can't be reached with Tab (no tabindex or role, measured live).
- The row link has no unread state.
- The unstarred star button has no accessible name and is `opacity-0 group-hover:opacity-100` without `group-focus-within`, so it stays invisible when focused.
- There is no `aria-live` region for list or sync changes.
- The focus ring is 1px, which may fall short of WCAG 2.2's focus appearance on night-paper (not measured).

**Multi-account privacy power user:**
- The 2px account bar is the only per-row account identifier, and Google and Apple share the same purple.
- It's unclear whether Inbox under "All accounts" is unified.
- There's no visible local-first or sync state.
- A spam email in the desktop reader showed rendered images with no blocked or allowed indicator in the reader chrome (cause unverified).

## Minor Observations

- Compose is a 32px neutral chip beside the logo, visually weaker than the refresh button.
- Empty icon slots are reserved for attachment, unread, thread and star (`maillist.templ` ~998, ~1005, ~1041), so the date stops about 100px short of the pane edge.
- Repeated label chips ("Avec/Handle Later") eat subject space.
- Reader: star, trash and unread are duplicated in the toolbar and per message. The links use pipe separators. The Dark/Light/Original switch shows even on a one-line message.
- The magenta "Current" badge reads like a warning, but it is neutral information.
- "Tags" is indented about 6px off its siblings. Account group labels outweigh the folders.
- Previews keep runs of underscores and punctuation from spam bodies.
- The Filters panel is about 800px wide with no backdrop. Apply is an outline button, and about 60% of the panel is empty.
- Eyebrow tracking is 0.12em live, against the documented 0.05em, and weight is 700 against 600.

## Questions to Consider

- If the unread dot were deleted, could weight and tone alone carry unread? Would that be clearer or louder than today?
- Where on the Mail surface does a stranger learn that their mail stays on their machine? What would a one-line sync/"on this device" footer cost in calm?
- Is the three-pane, phone-width list the right home for the density you promise, or does table view want a wider list or a bottom reader?
