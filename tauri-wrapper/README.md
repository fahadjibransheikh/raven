# Gofer desktop wrapper (Tauri v2)

This folder wraps the existing Gofer Go server in a real macOS `.app`: a Dock
icon and its own window pointed at `http://127.0.0.1:8090`, instead of a
browser tab. **It reimplements none of Gofer's UI.** On launch it:

1. Spawns the compiled Gofer binary as a background "sidecar" process.
2. Polls `127.0.0.1:8090` until it accepts connections (15s timeout).
3. Points the one native window at that URL.
4. Kills the Gofer process when you close the window / quit the app.

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
tauri-wrapper/src-tauri/target/release/bundle/macos/Gofer.app
tauri-wrapper/src-tauri/target/release/bundle/dmg/Gofer_0.1.0_aarch64.dmg
```

Drag the `.app` to `/Applications` (or double-click the `.dmg`) as usual.

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
    Cargo.toml                     <- tauri + tauri-plugin-shell deps
    build.rs                       <- required tauri-build hook
    tauri.conf.json                <- v2 config: window, externalBin, bundle
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
