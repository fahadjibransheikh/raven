# Gofer desktop wrapper (Tauri v2)

This folder wraps the existing Gofer Go server in a real macOS `.app`: a Dock
icon and its own window pointed at `http://127.0.0.1:8090`, instead of a
browser tab. **It reimplements none of Gofer's UI.** On launch it:

1. Spawns the compiled Gofer binary as a background "sidecar" process.
2. Waits for `127.0.0.1:8090` to accept connections for as long as the sidecar
   process is alive (a schema migration can take minutes). If the sidecar exits
   first, the window shows its last output and a "Try again" button.
3. Points the one native window at that URL.
4. Kills the Gofer process when you quit the app (tray Quit or Cmd+Q).
   Closing the window only hides it to the tray; the Dock icon or the tray's
   Show Raven brings it back.

Gofer's port is read from this repo's `gofer/.env` (`GOFER_ADDR=127.0.0.1:8090`)
at the time this was scaffolded. If you ever change `GOFER_ADDR`, update the
`GOFER_HOST` / `GOFER_PORT` constants near the top of
`src-tauri/src/lib.rs` to match.

> **Honesty note:** this was scaffolded in an environment with no Rust/Xcode
> toolchain, so none of it has been compiled or run. Every config key and
> Rust API used here (`externalBin`, the `shell:allow-execute` permission
> shape, `WebviewWindow::navigate()`, `RunEvent::Exit`, etc.) was checked
> against the current [v2.tauri.app](https://v2.tauri.app) docs rather than
> guessed from memory, but your **first `cargo tauri build` is the real
> test** -- it may well surface something (a Cargo version bump, a renamed
> API) that needs a small fix. If it does, paste the error back to Claude.

## One-time setup (macOS, in a real Terminal)

Run these from anywhere; they install the toolchain once for your machine,
not just this project.

```sh
# 1. Rust (Tauri's own docs recommend rustup over `brew install rust`)
curl --proto '=https' --tlsv1.2 https://sh.rustup.rs -sSf | sh
# then restart your terminal, or: source "$HOME/.cargo/env"

# 2. Xcode Command Line Tools (compiler/linker for macOS targets)
xcode-select --install

# 3. Tauri CLI -- pick ONE of these:
#    (a) via npm, since you already have Node:
npm install -g @tauri-apps/cli@latest
#    (b) or via cargo (slower first install, no Node dependency):
cargo install tauri-cli --version "^2.0.0" --locked
```

If you installed via npm, run `tauri <command>` below. If you installed via
cargo, run `cargo tauri <command>` instead -- they take the same arguments.

## Build the Gofer binary and stage it as the sidecar

Tauri bundles a prebuilt binary rather than compiling Gofer itself, so build
it with Gofer's own Taskfile first, from the **repo root** (`gofer/`, one
level above this folder):

```sh
cd gofer               # the actual repo root (contains Taskfile.yml, main.go)
task release           # produces ./dist/gofer
```

Tauri's sidecar mechanism expects the binary's filename to end in your
Rust *target triple*. Find yours:

```sh
rustc --print host-tuple
# Apple Silicon Macs -> aarch64-apple-darwin
# Intel Macs         -> x86_64-apple-darwin
```

Then copy it in (replace the triple below if yours differs):

```sh
cp dist/gofer tauri-wrapper/src-tauri/binaries/gofer-aarch64-apple-darwin
chmod +x tauri-wrapper/src-tauri/binaries/gofer-aarch64-apple-darwin
```

Re-run these two commands (`task release` + `cp`) any time you ship a new
Gofer version -- the wrapper always launches whatever binary is sitting in
`src-tauri/binaries/`.

## Generate the full icon set

`src-tauri/icons/icon.png` is currently just a copy of `assets/logo.png`.
Generate the macOS `.icns` (and other sizes Tauri wants) from it:

```sh
cd tauri-wrapper
tauri icon icons/icon.png       # or: cargo tauri icon icons/icon.png
```

## Run it

```sh
cd tauri-wrapper
tauri dev              # live window, for testing
tauri build             # produces the real .app / .dmg
```

`tauri build` output lands under:

```
tauri-wrapper/src-tauri/target/release/bundle/macos/Raven.app
tauri-wrapper/src-tauri/target/release/bundle/dmg/Raven_<version>_aarch64.dmg
```

Drag the `.app` to `/Applications` (or double-click the `.dmg`) as usual.

## Desktop-native features

Beyond the plain window wrapper described above, this app adds a few things
a browser tab can't give you:

- **Dock/taskbar unread badge.** An init script (`src-tauri/src/unread_badge.js`)
  polls Gofer's `/api/folders/unread` every 30s (plus on load and when the
  window becomes visible again) and writes the inbox count into
  `document.title` as a `(N) ` prefix. `on_document_title_changed` in
  `lib.rs` reads that prefix back out and calls `set_badge_count` on the
  window, so the count shows up on the Dock icon (macOS) / taskbar (Windows,
  Linux). Clears itself when the count is 0.
- **Tray icon.** A menu bar / system tray icon with "Show Raven", "Compose",
  "Check for Updates...", and "Quit". "Show" brings the window to the front; "Compose" does the same
  and then calls Gofer's own `openNewCompose()` JS function; "Quit" exits the
  app (which also kills the Gofer sidecar, same as closing the window).
- **Global shortcut.** `Cmd/Ctrl+Shift+M` opens the composer from anywhere,
  even when Raven isn't focused -- same behavior as the tray's "Compose".

## Releasing

The app version lives only in `src-tauri/Cargo.toml` (`tauri.conf.json` has no
`version`, so Tauri falls back to it). To ship a release:

1. Bump `version` in `src-tauri/Cargo.toml` and commit.
2. Tag it `vX.Y.Z` (must match Cargo.toml; CI fails otherwise) and push the tag.

CI (`.github/workflows/build-desktop.yml`) does the rest: drafts a release,
builds macOS/Windows/Linux, signs and uploads the updater artifacts and
`latest.json`, publishes the release, then bumps `Casks/raven.rb`. Edit the
release notes afterwards ("Release notes to follow" is a placeholder). Running
the workflow by hand (`workflow_dispatch`) only builds and uploads artifacts.

The updater signing key is at `~/.tauri/raven.key` (password in
`~/.tauri/raven.key.password`) and is stored as the repo secrets
`TAURI_SIGNING_PRIVATE_KEY` and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`. **If it
is lost, installed copies can no longer update** -- the public key is baked
into every build.

## Updates

Raven checks GitHub Releases (`releases/latest/download/latest.json`) on launch
and offers to install a newer version; the tray item "Check for Updates..."
does the same on demand and also reports "up to date" or errors. Installing
stops the bundled Gofer, replaces the app, and relaunches. On Linux only the
AppImage can update itself; for .deb/.rpm the launch check is skipped and the
tray item opens the releases page instead.

## Homebrew

```sh
brew tap fahadjibransheikh/raven https://github.com/fahadjibransheikh/raven
brew install --cask fahadjibransheikh/raven/raven
```

The cask (`Casks/raven.rb` at the repo root) is updated by CI on each release.

## Where the app keeps its settings and mail

Gofer reads `.env` and stores `data/` in its working directory. The app
always uses the per-user app data folder for that, on every machine:
`~/Library/Application Support/com.fahadsheikh.raven` on macOS,
`~/.local/share/com.fahadsheikh.raven` on Linux. The repo is never used, even
on the machine that built the app. Without a `.env` there, Gofer runs with its
defaults (local, no login). To configure it, copy `.env.example` into that
folder as `.env`.

## Troubleshooting

- **Window opens but stays blank / stuck on "Starting Gofer...":** almost
  always means the sidecar never started or never reached port 8090.
  - Check `src-tauri/binaries/` actually contains a file named
    `gofer-<your-exact-target-triple>` and that it's executable
    (`chmod +x`). This is the #1 likely mistake for a Tauri beginner --
    the filename must match `rustc --print host-tuple` exactly, including
    on an M-series Mac running an Intel-built Gofer binary under Rosetta
    (in which case use `x86_64-apple-darwin`, matching however you built
    the Go binary, not the CLI's own host triple).
  - Try running that binary directly from a terminal
    (`./src-tauri/binaries/gofer-aarch64-apple-darwin`) to see Gofer's own
    startup errors (e.g. a port already in use by something unrelated, or a
    missing `.env`).
  - Run `tauri dev` (not `build`) and watch the terminal -- `lib.rs` prints
    an `eprintln!` if the 15s poll times out or if `navigate()` fails.
- **Window loads but looks broken / requests seem blocked:** check
  `src-tauri/capabilities/default.json` and `app.security` in
  `tauri.conf.json`. This wrapper deliberately does almost nothing here
  (see the comment in `default.json`): the sidecar spawn/kill and the
  `window.navigate()` call are made directly from Rust in `lib.rs`, not via
  `invoke()` from frontend JS, so Tauri's permission system (which only
  gates the JS<->Rust IPC bridge) doesn't need to grant anything for them.
  Gofer's page also never calls a Tauri API, so there's no CSP concern
  either -- once navigated, the webview is just showing a normal
  `http://127.0.0.1:8090` page, the same as opening it in Safari. If this
  turns out to be wrong in practice, the fix is almost certainly either
  loosening/adding an entry to `permissions` in `default.json`, or setting
  `app.security.csp` explicitly (it's currently left unset/null, i.e. no
  CSP is injected at all).
- **App quits but Gofer keeps running (orphaned process):** shouldn't
  happen -- `lib.rs` kills the sidecar on both `WindowEvent::CloseRequested`
  and `RunEvent::Exit` -- but if you ever see it, check
  `ps aux | grep gofer` and `kill` it manually, then tell Claude so the
  lifecycle handling can be tightened.
- **`task release` isn't found / fails:** that's Gofer's own build, unrelated
  to this wrapper -- see the main `gofer/Taskfile.yml` and `gofer/README.md`.

## What's deliberately NOT here

- **No `package.json`** at this folder's root. Tauri v2's `frontendDist` can
  point at a plain static folder (`../frontend`, just one placeholder
  `index.html`, no build step), so no Node/npm build tooling is required for
  this wrapper itself -- confirmed against the v2 config docs rather than
  assumed. (You still need Node if you chose the npm install path for the
  Tauri CLI above, but that's global, not per-project.)
- **No hand-built `.icns`.** Run `tauri icon` (see above) instead of trying
  to generate the macOS icon set by hand.

## Files in this folder

```
tauri-wrapper/
  README.md                        <- this file
  frontend/
    index.html                     <- tiny "Starting Gofer..." placeholder;
                                        replaced by navigate() at runtime
  src-tauri/
    Cargo.toml                     <- tauri + plugin deps; the app version
    build.rs                       <- required tauri-build hook
    tauri.conf.json                <- v2 config: window, externalBin, bundle, updater
    capabilities/default.json      <- permissions for the main window
    icons/icon.png                 <- copy of ../../assets/logo.png (source
                                        for `tauri icon`)
    binaries/                      <- put gofer-<target-triple> here (gitignored,
                                        build output -- see .gitkeep)
    src/
      main.rs                     <- thin entry point
      lib.rs                      <- all the actual logic (spawn/poll/
                                        navigate/kill), see its doc comment
```
