# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The same web UI ships three ways: in a browser, inside the Tauri desktop app (`tauri-wrapper/`) on macOS, Windows and Linux, and as an installed phone PWA when Raven runs on a reachable server. The Tauri app wraps the web UI and does not use a native design language.

## Users

Power users who care about privacy and handle several mail accounts (Gmail, Outlook, and generic IMAP/SMTP) along with calendars and contacts. They live in email all day and triage fast. They want their data on their own machine, not in a third-party mail service's cloud. The repo owner is one of them and uses Raven daily on Mac and phone.

## Product Purpose

Raven is a local-first email client with a built-in calendar and contacts. It runs on the user's own machine, keeps mail and related data local in SQLite, and talks directly to providers: IMAP/SMTP, CalDAV and CardDAV for generic accounts, the Gmail, Google Calendar and Google People APIs for Gmail, and Microsoft Graph for Outlook. Success means a power user can run all their accounts from Raven every day and never miss the provider's own client.

## Positioning

- **Local-first, no middleman:** data stays on the user's machine, and Raven connects straight to each provider. No vendor cloud relays or stores mail.
- **One calm app for all accounts:** mail, calendar and contacts across Gmail, Outlook and IMAP live in one clean place instead of one client per provider.
- **Speed and keyboard flow:** fast triage, keyboard shortcuts, and a global compose shortcut (`Cmd/Ctrl+Shift+M`).

## Operating Context

- Used all day as the primary mail client, mostly in the desktop app, plus the phone layout with Web Push notifications.
- Mail: threads, attachments, drafts with autosave, signatures, sending identities, scheduled send, one-click unsubscribe, translation.
- Organisation: folders, labels, stars, archive, spam controls, select-all across a folder, advanced search filters.
- Calendar: day, 3-day, week and month views, agenda list, event creation, and invitations answered from the inbox. On macOS, an agenda widget shows the next 3 days.
- Contacts: local address books, vCard import/export, provider sync.
- Access modes: no-login local use, one protected personal profile, or managed users with separate administrators.

## Capabilities and Constraints

- Stack: Go, templ views, HTMX-style interactions, Tailwind, SQLite; house UI components live in `components/`.
- Pages run under CSP `script-src 'self'`: no inline scripts and no `on*`/`hx-on` handlers. Behaviour goes through `data-on-*` attributes and `actions.js`.
- Security: credentials are stored encrypted, and remote content is blocked by default. When allowed, remote content loads through Raven. Optional sign-in supports passwords, TOTP, passkeys, or external providers.
- Status: alpha. Expect change.
- Raven is its own product. It split from upstream gofer on 2026-10-15, and there are no upstream syncs.

## Brand Commitments

- Name: Raven. Logo: `assets/logo.svg`.
- Single "raven" layout. The classic and minimal themes are colour/accent presets only, never separate structures, and themes differ only in colour tokens (decided 2026-10-06).
- The user named Notion Mail and Microsoft Outlook as references: Notion Mail for calm minimalism, Outlook for density and clarity.
- README voice: plain, direct, lowercase section headings.

## Evidence on Hand

- Screenshots in `screenshots/` (emails card/table views, contacts, settings in classic and minimal themes, light and dark). They use made-up demo data.
- No testimonials, user counts, press, or benchmarks exist. Do not fabricate any.

## Product Principles

1. Your data, your machine: never design a flow that implies a Raven cloud or a middleman.
2. Triage speed beats decoration: every common action should have a fast, keyboard-reachable path.
3. Many accounts should feel like one calm inbox, with each account still identifiable (account colours).
4. Dense but calm: show a lot without noise.
5. One structure everywhere: theming changes colour, not layout.

## Accessibility & Inclusion

WCAG 2.2 AA baseline: contrast, full keyboard operation, and screen-reader support. This matters for an app used all day.
