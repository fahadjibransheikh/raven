<!--
Release notes shown in the in-app "What's New" window, newest first.
One entry per version:  ## VERSION | YYYY-MM-DD | Title
Then an optional hero line, then "### New", "### Improved", "### Fixed" or
"### Security" groups of "- " bullets, in plain user language.
"next" is the placeholder for the unreleased build; the release process
replaces it with the real version number. GitHub release notes use the same text.
-->

## 1.2.0 | 2026-10-21 | Meeting reminders and a calmer, phone-friendly Raven
Meeting reminders now stay on screen with a Join button, replies open right below the conversation, and Raven is easier to use on a phone.

### New
- Meeting reminders: Raven reminds you before a timed event starts. While Raven is open, a card stays on screen with Join meeting and Open calendar until you close it. Choose the reminder time in Settings › Advanced. On a Mac, set Raven to Alerts in System Settings › Notifications so the system notification stays too.
- Replies and forwards open right below the conversation instead of over it. New messages still open in their own window.
- On a phone, a bar at the bottom switches between Mail, Calendar and Contacts, and message actions sit at the bottom within thumb reach.
- Color mode has a System option that follows your computer's light or dark setting.

### Improved
- After you press Send, the message closes straight away and shows "Message sent" with Undo, instead of keeping you on the message for the undo time.
- The calendar agenda groups events by day in compact rows, and a calendar with nothing connected shows one clear "Connect a calendar" step.
- The first screen explains that your mail stays on the computer running Raven and that passwords are stored encrypted.
- Settings, Contacts and adding an account are simpler: fewer boxes, plainer words, empty contact fields hidden, and Contacts sync off by default for new accounts.
- A search stays in the search box so you can edit it, and "No results" offers to clear the search and filters.
- Screen readers and keyboards: every button is named, focus is easier to see, and a "Skip to main content" link comes first.
- Text sizes are more even and the dark-mode pink is softer. The Translate button now says Google does the translating.

### Fixed
- Outlook mail stopped syncing after the computer slept. It now reconnects on its own.
- Join meeting links did nothing in the desktop app. They now open in your browser.

## 1.1.3 | 2026-10-19 | Clearer mail list
The mail list is easier to scan: unread mail stands out, sent mail shows who you wrote to, and you can see when mail last synced.

### Improved
- Unread messages now have a bold sender and subject instead of differing only by colour, and screen readers say "Unread".
- Sent and Drafts show who you wrote to, such as "To Ana +2", instead of your own name.
- The bottom of the mail list shows when mail last synced.
- Table view gives the Subject column the most room by default. Column widths you set yourself are kept.
- Message previews skip the rows of dashes and underscores that newsletters use as dividers.
- A new account gets a colour none of your other accounts use.
- Sidebar and list text is a little larger and more even, and Apply stands out in the filters.
- The star button can be reached with the keyboard and is named for screen readers.

### Fixed
- On a phone, the Compose button above the mail list did nothing. It now opens a new message.

## 1.1.2 | 2026-10-19 | Undo send, snooze and safer links
Take back a message after sending, snooze mail until later, and get warned about disguised links and senders.

### New
- Undo send: after you press Send, Raven waits 10 seconds and offers Undo, which reopens the message. Choose 5 to 30 seconds, or Off, in Settings.
- Snooze a conversation until later today, tomorrow, the weekend, next week or a time you pick. It leaves your inbox, waits under Snoozed in the sidebar and comes back with a notification. Use the alarm-clock button or press B. Snoozing happens in Raven only, so other mail apps still show the message.
- Follow-up reminders: Raven can remind you about a message on a date you choose, only if nobody has replied by then.
- When new mail has a sign-in or verification code, the notification shows the code first and Raven offers a Copy button. The open message shows the code with its own copy button.
- A warning appears above a message when the sender looks like someone you know but isn't, such as a look-alike domain or a name that shows a different address.

### Improved
- Links in emails no longer carry tracking tags such as utm_, and invisible tracking pixels are removed.
- If a link's text shows one web address but it leads somewhere else, Raven asks before opening it. Newsletter links and links protected by Microsoft 365, Proofpoint or Mimecast open as before.

### Fixed
- Gmail and Outlook accounts never showed new-mail notifications. They now notify like other accounts.
- On Mac, an email you opened could stay marked unread for 2 to 3 seconds. It now shows as read straight away.

## 1.1.1 | 2026-10-16 | Widget, search and Outlook fixes
A tidier Mac widget, more exact search and faster Outlook accounts.

### Improved
- Outlook and Microsoft 365 accounts asked Microsoft for a new sign-in token before almost every mail, contacts and calendar request. Raven now reuses each token until it is about to expire, so these accounts respond faster.

### Fixed
- On Mac, the agenda widget could cut off its last event and the "+N more" line, and the large widget could end on a day heading with no events under it. Events now fit the widget at every size.
- Searching for text with % or _ in it, such as "50%" or "first_name", also matched messages and contacts that did not contain it. Search now matches these characters exactly.

## 1.1.0 | 2026-10-15 | A 3-day calendar, a Mac widget and many fixes
Calendar opens on your next 3 days, Mac gets an agenda widget, and mail, sending and sync are more reliable.

### New
- Calendar now opens on the next 3 days on an hour grid, with a red line at the current time. Day and 3 Days are also in the view switcher.
- On Mac, a Raven widget shows your events for the next 3 days. Add it once from the widget gallery (right-click the desktop, then Edit Widgets). Clicking it opens the calendar.

### Fixed
- Some attachments never appeared, such as a PDF placed inline or a file with a very long name, and an attached text file could replace the message itself. Attachments now always show up, and messages sent in several parts show in full.
- After a big clean-up in another mail app, a folder could stop getting new mail, and emptying a very large Trash left the deleted mail on screen. Both now work at any size.
- Scheduled emails arrived dated the day you scheduled them, and some Microsoft 365, Outlook.com and Yahoo accounts got two copies of each sent message. Scheduled mail now carries the time it went out, and Sent gets one copy.
- Search missed some replies, and after: and before: dates followed UTC instead of your time zone. Both now find what you expect.
- Moving "select all" across two accounts moved some messages, then said the change was undone. Raven now refuses the move before moving anything.
- In dark mode, some emails showed black text on the dark background. The text now switches to a light colour.
- On Mac, if Raven's background server stopped, the window stayed on an error until you quit, and a failed update left the app unusable. Raven now restarts it on its own.
- On a shared Raven server, sync could stall with no error, contacts and calendar sync sometimes failed with "database is locked", and one slow mail server held up everyone's outgoing mail. All three are fixed.

### Security
- On a shared Raven server, images in an email could make the server fetch from a private calendar server the administrator had allowed, and an oversized request from a signed-in user could crash the server. Both are blocked.
- Importing a contacts file could add photos that told a third party when you looked at the contact. Imported contacts now keep only photos stored inside the file.

## 1.0.2 | 2026-10-14 | Your own profile photo
Your Gmail and Google Workspace accounts now show your own Google profile photo.

### Fixed
- Google Workspace accounts showed the company's logo as your picture, and personal Gmail accounts could show only a letter. Raven now uses the profile photo from your Google account wherever your address appears. Existing accounts switch over on their own shortly after updating.

## 1.0.1 | 2026-10-14 | Add Gmail with Google sign-in
Anyone using the desktop app can now add a Gmail account by signing in with Google.

### Fixed
- Adding a Gmail account with "Sign in with Google" in the desktop app failed with "google oauth not configured" unless you had set up your own Google sign-in. It now works out of the box. Google may say it hasn't verified Raven yet; choose Advanced, then continue to Raven.
- If Google or Microsoft sign-in isn't set up on a Raven server, the add-account window now says so instead of showing an option that fails.
- On iPhone, adding Raven to the Home Screen always used the default icon. It now uses the app icon you picked under Settings, Appearance. Remove and re-add the Home Screen icon to pick up a new one.

## 1.0.0 | 2026-10-13 | Raven 1.0
Raven reaches 1.0: one place for your mail, contacts and calendar, on your desktop and your phone. Here is what that covers.

### New
- Mail for Gmail, Outlook, iCloud and any IMAP account in one inbox, or one account at a time.
- A calendar for Google, Outlook and iCloud with month and week views, an agenda, and creating, editing and answering events.
- Answer meeting invitations with Yes, Maybe or No right from the email.
- Contacts from all your accounts in one list, kept in sync.
- A command bar (Cmd+K, or Ctrl+K on Windows and Linux) and keyboard shortcuts for mail and calendar; press ? to see them.
- One-click unsubscribe, scheduled sending, send-as addresses and Undo for archive, delete and move.
- Use Raven from your phone's browser with a layout built for touch, swipe actions and notifications.
- Choose your app icon and accent colour under Settings, Appearance.

### Fixed
- On Mac, the top of emails with pictures could turn blank right after opening, leaving empty space above the message until you scrolled. Emails now stay fully visible when they open.

## 0.3.4 | 2026-10-13 | A new calendar, a new logo, and a faster reader
A rebuilt calendar, a new Raven logo you can recolour, and email that opens faster.

### New
- A new calendar for Google, Outlook and iCloud: month and week views, an agenda, recurring events, and creating, editing and answering events. The calendars you had selected carry over (Outlook needs one tick under Settings, then your Microsoft account, then Calendar).
- Answer meeting invitations with Yes, Maybe or No right from the email, even before the event reaches your calendar.
- A new Raven logo. Choose Raven, Classic or Minimal under Settings, Appearance, App icon; the Dock and browser tab icon switch with your light or dark theme.
- Select every message in a folder at once, not only the ones on screen, and expand or collapse a whole conversation with one click.
- Account setup tests the connection before saving, so a typo in a server name shows up right away.

### Improved
- Emails with many pictures open much faster: pictures are kept after the first view and fetched ahead when you hover over a message.
- Reply, Reply all and Forward sit on every message in a conversation, even collapsed ones, and quoted text folds behind a tidier toggle.
- Settings pages share one card layout with their own titles; mail, contacts and calendar each remember their own column width.
- On phones: swipe mail rows, a calendar with its own top bar and agenda screen, and a step bar in account setup.
- Press ? on the calendar to see its shortcuts: T for today, J and K to move, M and W for month and week, C for a new event.

### Security
- Pictures in emails now load through Raven instead of straight into the reader, and pages inside Raven run only Raven's own scripts.
- Contacts and calendar servers on your own network work as before; any other private address needs an exception under Settings, Mail security.

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
