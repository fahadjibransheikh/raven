<!--
Release notes shown in the in-app "What's New" window, newest first.
One entry per version:  ## VERSION | YYYY-MM-DD | Title
Then an optional hero line, then "### New", "### Improved", "### Fixed" or
"### Security" groups of "- " bullets, in plain user language.
"next" is the placeholder for the unreleased build; the release process
replaces it with the real version number. GitHub release notes use the same text.
-->

## next | 2026-10-06 | A fresh look, Undo, and a safer email viewer

A redesigned Raven, an Undo for your mail actions, and a long list of fixes.

### New
- Right-click Spam or Trash in the sidebar to empty it.
- Select all with the new checkbox above the message list, then use Move to… (shortcut v) to file one or many messages in any folder.
- Undo after you archive, delete, mark as spam or move a message.
- A refreshed design inspired by Notion Mail and Outlook: a cleaner sidebar, list and reader, with accent color presets in Settings.
- What's New, this window. Open it again any time from the ? menu or the Cmd/Ctrl+K command palette.

### Fixed
- Outlook accounts could grow the local database to many gigabytes. Raven now cleans it up automatically the first time you launch after updating, which may take a minute.
- Pictures inside emails now show when you sign in with a password.
- Replying puts the cursor in the message, replies go to the right people (including mail from your own address and senders with a Reply-To), and Send no longer delivers a message twice.
- Failures that used to pass silently now show a message, and the confirmation prompts that stopped appearing (such as Empty Spam and discarding a draft) are back.

### Security
- A safer email viewer: message content is now fully contained and can no longer run code or reach the rest of the app.
- Remote images and other remote content are blocked by default for new users. Existing settings are unchanged.
- Other protections against malicious mail and links, and updated components with known vulnerabilities.
