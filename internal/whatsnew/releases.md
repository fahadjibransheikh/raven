<!--
Release notes shown in the in-app "What's New" window, newest first.
One entry per version:  ## VERSION | YYYY-MM-DD | Title
Then an optional hero line, then "### New", "### Improved", "### Fixed" or
"### Security" groups of "- " bullets, in plain user language.
"next" is the placeholder for the unreleased build; the release process
replaces it with the real version number. GitHub release notes use the same text.
-->

## 0.3.0 | 2026-10-09 | A quieter inbox, keyboard-first
A cleaner, quieter look, a command bar for everything, and one-click unsubscribe.

### New
- Press Cmd+K (Ctrl+K on Windows and Linux) to open the command bar: archive, reply, move, label, unsubscribe, or jump to any folder, by typing a few letters.
- Keyboard shortcuts now show where you work: key hints sit on the reader's buttons and below the reply box. Press L to label a message.
- Unsubscribe from mailing lists with one button. Raven sends the request for you, or emails the list from the address it was sent to.
- Download any email as an .eml file, or print it, from the reader's More menu.

### Improved
- A quieter design: the message list and reader float as panels, pink is kept for unread mail, Compose is a small button beside the logo, and Mail, Contacts and Calendar sit at the bottom of the sidebar.
- The message list drops avatars and bold text. A thin colour bar shows which account each email came from, and today's times use 24-hour format. Want avatars back? Drag them in under the list's card layout.
- Account folders start collapsed under All accounts, so the sidebar stays short.
- The message list can now be made much narrower by dragging its edge.
- Replies to mail you received as Bcc or through a forwarding address now suggest the right "from" address.
- The app is called Raven everywhere, including the macOS menu and Activity Monitor. On macOS, Compose and Check for Updates moved from the menu bar icon to the app menu.

### Fixed
- Email times were wrong after you changed time zones, such as when travelling. Raven now follows your device's current time zone. If you had picked a time zone in Settings, choose it again.
- The Reply and Forward items in the reader's More menu did nothing. They now work.

## 0.2.1 | 2026-10-06 | Calmer inbox, newest-first conversations
A calmer, roomier message list, conversations that open on the latest email, and a sturdier desktop app.

### New
- A calmer message list: two lines per message, with subject and preview on one line. Prefer more space? Choose Airy under Settings → Appearance → List density.
- Conversations now show the newest email at the top. Prefer the classic order? Switch it under Settings → Compose and display → Conversation order.
- Raven can open email links (mailto:) and start a new message.
- Closing the window keeps Raven running in the tray, so new mail still arrives. Quit from the tray or with Cmd+Q.

### Improved
- Raven now checks for updates every few hours while it runs, and Linux .deb and .rpm installs can update themselves.
- Faster everyday actions: marking read, starring, archiving and moving are many times quicker in large folders, and pages load lighter.
- A cleaner list: archive, delete and mark-as-read appear only when you select messages, dates line up, and stray quotes around sender names are gone.

### Fixed
- After an update, Raven could sit on "Starting Raven…" while it upgraded your mailbox. It now waits and shows what it is doing, or explains what went wrong.
- The Security and Advanced tabs in Settings were blank when Raven runs without a login. They now show their content.
- Leaving an unsaved draft for another folder, message or Settings now asks whether to keep it.

### Security
- In the desktop app, other programs on your computer can no longer read or send your mail through Raven.
- Stronger protection against malicious mail, tighter sign-in limits, and safer handling of contacts and avatar lookups.

## 0.2.0 | 2026-10-06 | A fresh look, Undo, and a safer email viewer

A redesigned Raven, an Undo for your mail actions, and a long list of fixes.

### New
- Right-click Spam or Trash in the sidebar to empty it.
- Select all with the new checkbox above the message list, then use Move to… (shortcut v) to file one or many messages in any folder.
- Undo after you archive, delete, mark as spam or move a message.
- A refreshed design inspired by Notion Mail and Outlook: a cleaner sidebar, list and reader, with accent color presets in Settings.
- What's New, this window. Open it again any time from the ? menu or the Cmd/Ctrl+K command palette.

### Improved
- Faster and lighter: a smaller download, quicker loading, and no more stale screens after an update.

### Fixed
- Outlook accounts could grow the local database to many gigabytes. Raven now cleans it up automatically the first time you launch after updating, which may take a minute.
- Pictures inside emails now show when you sign in with a password.
- Replying puts the cursor in the message, replies go to the right people (including mail from your own address and senders with a Reply-To), and Send no longer delivers a message twice.
- Failures that used to pass silently now show a message, and the confirmation prompts that stopped appearing (such as Empty Spam and discarding a draft) are back.

### Security
- A safer email viewer: message content is now fully contained and can no longer run code or reach the rest of the app.
- Remote images and other remote content are blocked by default for new users. Existing settings are unchanged.
- Other protections against malicious mail and links, and updated components with known vulnerabilities.
