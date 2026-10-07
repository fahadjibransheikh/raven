//! Gofer desktop wrapper.
//!
//! This crate is deliberately NOT a UI. It exists only to:
//!   1. Launch the compiled Gofer Go binary (bundled as a Tauri "sidecar",
//!      see https://v2.tauri.app/develop/sidecar/) as a background process.
//!   2. Wait for it to start listening on 127.0.0.1:8090.
//!   3. Point the one native window at http://127.0.0.1:8090, so the user
//!      gets Gofer's existing server-rendered UI inside a real macOS app
//!      window (Dock icon, Cmd+Q, its own process) instead of a browser tab.
//!   4. Kill the Gofer process when the window closes / the app quits, so
//!      nothing is left running in the background.
//!
//! Gofer's own HTML/HTMX frontend is loaded as plain remote content -- it
//! never calls any Tauri API -- so there is intentionally no IPC/frontend
//! logic here beyond the tiny "Starting Gofer..." placeholder page in
//! ../frontend/index.html that this window shows for the second or two
//! before the navigate() call below replaces it.

use std::net::TcpStream;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Manager, RunEvent, WindowEvent};
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;
use tauri_plugin_updater::UpdaterExt;

/// Injected into the main window; polls Gofer's unread count and reflects it
/// in `document.title` as a "(N) " prefix, which `on_document_title_changed`
/// below parses back out into the Dock/taskbar badge.
const UNREAD_BADGE_SCRIPT: &str = include_str!("unread_badge.js");

/// Host/port Gofer listens on. This matches `GOFER_ADDR=127.0.0.1:8090` in
/// the repo's `.env` (see .env / .env.example). If you change
/// GOFER_ADDR, update these two constants to match.
const GOFER_HOST: &str = "127.0.0.1";
const GOFER_PORT: u16 = 8090;
const GOFER_URL: &str = "http://127.0.0.1:8090";

/// Microsoft settings the desktop app always runs Gofer with, so Outlook works
/// on every install without editing a .env. The client ID is Raven's Azure app,
/// registered as a public client (no secret; PKCE protects the code exchange),
/// so it is safe to ship. Its redirect URIs are registered for
/// http://localhost:8090, hence the fixed base URL. Gofer's .env loader never
/// overrides variables that are already set, so these win over any stale
/// values in a user's .env.
const SIDECAR_ENV: [(&str, &str); 4] = [
    ("GOFER_BASE_URL", "http://localhost:8090"),
    ("MICROSOFT_OAUTH_CLIENT_ID", "57979fc5-6850-4d3d-8e8b-ab3122e4dc0c"),
    ("MICROSOFT_OAUTH_CLIENT_SECRET", ""),
    ("MICROSOFT_OAUTH_TENANT", "common"),
];

/// Working directory the sidecar is spawned with. Gofer reads its .env
/// (OAuth credentials) and its data/ SQLite store relative to its process
/// working directory, so this is the per-user app data folder
/// (~/Library/Application Support/<identifier> on macOS,
/// ~/.local/share/<identifier> on Linux) on every machine, including the
/// one that builds the app. Never the repo: that is for development only.
fn gofer_working_dir(app: &AppHandle) -> std::path::PathBuf {
    let dir = app
        .path()
        .app_data_dir()
        .expect("failed to resolve the app data folder");
    std::fs::create_dir_all(&dir).expect("failed to create the app data folder");
    dir
}

/// How long to wait for Gofer to come up before giving up and leaving the
/// "Starting Gofer..." placeholder on screen. Schema migrations run before the
/// server listens and can take minutes on a large mailbox (v98's orphan-thread
/// cleanup took ~1 min on 1.5M rows), so this must outlast them.
const STARTUP_TIMEOUT: Duration = Duration::from_secs(30 * 60);
const POLL_INTERVAL: Duration = Duration::from_millis(200);

/// Managed app state: holds the spawned sidecar's child-process handle, if
/// this instance of the app is the one that spawned it. `None` covers two
/// cases: (a) we haven't spawned yet, or (b) Gofer was already running on
/// the port when we started (e.g. a previous copy of this app, or the user
/// running `task dev`/`./dist/gofer` manually), so we never spawned our own
/// copy and there is nothing for us to kill on exit.
struct SidecarState(Arc<Mutex<Option<CommandChild>>>);

fn port_is_open() -> bool {
    TcpStream::connect((GOFER_HOST, GOFER_PORT)).is_ok()
}

fn wait_for_port(timeout: Duration) -> bool {
    let start = Instant::now();
    while start.elapsed() < timeout {
        if port_is_open() {
            return true;
        }
        std::thread::sleep(POLL_INTERVAL);
    }
    false
}

fn kill_sidecar(app_handle: &tauri::AppHandle) {
    let state = app_handle.state::<SidecarState>();
    let child = state.0.lock().unwrap().take();
    if let Some(child) = child {
        // Best-effort: if the process already exited, this can fail; that's
        // fine, there's nothing to clean up.
        let _ = child.kill();
    }
}

const RELEASES_URL: &str = "https://github.com/fahadjibransheikh/raven/releases/latest";

/// Tauri's updater can only replace an AppImage on Linux (it swaps the file
/// `$APPIMAGE` points at). A .deb/.rpm install is owned by the package
/// manager, so there the user has to install the new package themselves.
fn can_self_update() -> bool {
    !cfg!(target_os = "linux") || std::env::var_os("APPIMAGE").is_some()
}

/// Shows a message dialog without blocking the caller's thread: the blocking
/// `show` variant is pushed onto the blocking pool, so this is safe to await
/// from the async runtime (and never touches the main/event-loop thread).
async fn dialog(
    app: &AppHandle,
    kind: MessageDialogKind,
    message: String,
    buttons: MessageDialogButtons,
) -> bool {
    let builder = app
        .dialog()
        .message(message)
        .title("Raven")
        .kind(kind)
        .buttons(buttons);
    tauri::async_runtime::spawn_blocking(move || builder.blocking_show())
        .await
        .unwrap_or(false)
}

async fn check_latest(
    app: &AppHandle,
) -> tauri_plugin_updater::Result<Option<tauri_plugin_updater::Update>> {
    app.updater()?.check().await
}

/// Checks GitHub Releases for a newer Raven and, if the user agrees,
/// downloads it, stops the sidecar, installs, and relaunches. `user_initiated`
/// is true for the tray item (report every outcome) and false for the launch
/// check (stay silent unless there is an update, so offline launches are quiet).
async fn check_for_update(app: AppHandle, user_initiated: bool) {
    if !can_self_update() {
        if user_initiated {
            if let Err(err) = tauri_plugin_opener::open_url(RELEASES_URL, None::<&str>) {
                eprintln!("failed to open {RELEASES_URL}: {err}");
            }
        }
        return;
    }

    let update = match check_latest(&app).await {
        Ok(update) => update,
        Err(err) => {
            eprintln!("update check failed: {err}");
            if user_initiated {
                dialog(
                    &app,
                    MessageDialogKind::Error,
                    format!("Couldn't check for updates: {err}"),
                    MessageDialogButtons::Ok,
                )
                .await;
            }
            return;
        }
    };

    let Some(update) = update else {
        if user_initiated {
            dialog(
                &app,
                MessageDialogKind::Info,
                format!(
                    "You're on the latest version ({}).",
                    app.package_info().version
                ),
                MessageDialogButtons::Ok,
            )
            .await;
        }
        return;
    };

    let accepted = dialog(
        &app,
        MessageDialogKind::Info,
        format!(
            "Raven {} is available (you have {}). Install and restart now?",
            update.version, update.current_version
        ),
        MessageDialogButtons::OkCancelCustom("Install and Restart".into(), "Later".into()),
    )
    .await;
    if !accepted {
        return;
    }

    let bytes = match update.download(|_, _| {}, || {}).await {
        Ok(bytes) => bytes,
        Err(err) => {
            eprintln!("update download failed: {err}");
            dialog(
                &app,
                MessageDialogKind::Error,
                format!("Couldn't download the update: {err}"),
                MessageDialogButtons::Ok,
            )
            .await;
            return;
        }
    };

    // Stop Gofer before files are replaced: on Windows a running gofer.exe is
    // locked and would make the installer fail.
    kill_sidecar(&app);
    if let Err(err) = update.install(bytes) {
        eprintln!("update install failed: {err}");
        dialog(
            &app,
            MessageDialogKind::Error,
            format!("Couldn't install the update: {err}\n\nRestart Raven to keep using it."),
            MessageDialogButtons::Ok,
        )
        .await;
        return;
    }
    app.restart();
}

/// Shared by the tray "Show"/"Compose" items and the global shortcut: bring
/// the main window to the front, and optionally trigger Gofer's own compose
/// UI via its existing top-level `openNewCompose()` JS function (the page
/// never calls Tauri IPC, so this is the only way in from the native side).
fn show_main_window(app_handle: &AppHandle, open_compose: bool) {
    let Some(window) = app_handle.get_webview_window("main") else {
        eprintln!("main window not found; could not show it");
        return;
    };
    let _ = window.unminimize();
    let _ = window.show();
    let _ = window.set_focus();
    if open_compose {
        let _ = window.eval("typeof openNewCompose==='function'&&openNewCompose()");
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        // The two below are driven only from Rust (check_for_update), never
        // via invoke(), so like the global shortcut they need no capability.
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_notification::init())
        // Remembers the main window's size, position and maximized state
        // across launches (saved on close/quit, restored when the window is
        // built in setup()). VISIBLE is left out so Raven always opens shown.
        .plugin(
            tauri_plugin_window_state::Builder::new()
                .with_state_flags(
                    tauri_plugin_window_state::StateFlags::all()
                        - tauri_plugin_window_state::StateFlags::VISIBLE,
                )
                .build(),
        )
        .plugin(
            // Rust-side registration only -- the page never calls Tauri IPC,
            // so no capability entry is needed (the plugin's default
            // permission set is empty; it only gates the JS invoke() bridge
            // this app doesn't use).
            tauri_plugin_global_shortcut::Builder::new()
                .with_shortcut("CmdOrCtrl+Shift+M")
                .expect("invalid global shortcut accelerator")
                .with_handler(|app, _shortcut, event| {
                    if event.state == tauri_plugin_global_shortcut::ShortcutState::Pressed {
                        show_main_window(app, true);
                    }
                })
                .build(),
        )
        .manage(SidecarState(Arc::new(Mutex::new(None))))
        .setup(|app| {
            let app_handle = app.handle().clone();

            // The main window is built here rather than auto-created from
            // tauri.conf.json ("create": false) because on_new_window is only
            // available on the builder. Email links carry target="_blank"
            // (see emailExternalLinksScript in internal/handler/handler.go);
            // the webview silently drops those by default, so hand them to
            // the system browser instead.
            let window_config = app
                .config()
                .app
                .windows
                .iter()
                .find(|w| w.label == "main")
                .expect("main window config missing from tauri.conf.json")
                .clone();
            tauri::WebviewWindowBuilder::from_config(&app_handle, &window_config)?
                .on_new_window(|url, _features| {
                    if matches!(url.scheme(), "http" | "https" | "mailto") {
                        if let Err(err) = tauri_plugin_opener::open_url(url.as_str(), None::<&str>) {
                            eprintln!("failed to open {url} externally: {err}");
                        }
                    }
                    tauri::webview::NewWindowResponse::Deny
                })
                .initialization_script(UNREAD_BADGE_SCRIPT)
                .on_document_title_changed(|window, title| {
                    // unread_badge.js prefixes the title with "(N) " when
                    // there's unread mail; parse that back out for the
                    // Dock/taskbar badge. No prefix (or N == 0) clears it.
                    let count = title
                        .strip_prefix('(')
                        .and_then(|rest| rest.split_once(')'))
                        .and_then(|(n, _)| n.parse::<i64>().ok())
                        .filter(|n| *n > 0);
                    if let Err(err) = window.set_badge_count(count) {
                        eprintln!("failed to set badge count: {err}");
                    }
                })
                .build()?;

            let show_item = MenuItem::with_id(app, "show", "Show Raven", true, None::<&str>)?;
            let compose_item = MenuItem::with_id(app, "compose", "Compose", true, None::<&str>)?;
            let updates_item = MenuItem::with_id(
                app,
                "check_updates",
                "Check for Updates\u{2026}",
                true,
                None::<&str>,
            )?;
            let quit_item = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
            let tray_menu = Menu::with_items(
                app,
                &[
                    &show_item,
                    &compose_item,
                    &updates_item,
                    &PredefinedMenuItem::separator(app)?,
                    &quit_item,
                ],
            )?;
            TrayIconBuilder::new()
                .icon(app.default_window_icon().cloned().expect(
                    "default window icon missing -- check tauri.conf.json bundle.icon",
                ))
                .menu(&tray_menu)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => show_main_window(app, false),
                    "compose" => show_main_window(app, true),
                    "check_updates" => {
                        tauri::async_runtime::spawn(check_for_update(app.clone(), true));
                    }
                    "quit" => app.exit(0),
                    _ => {}
                })
                .build(app)?;

            tauri::async_runtime::spawn(check_for_update(app_handle.clone(), false));

            // If Gofer is already listening (previous run, or started by
            // hand), don't spawn a second copy -- just adopt it.
            if !port_is_open() {
                let sidecar_command = app_handle.shell().sidecar("gofer").expect(
                    "failed to resolve the `gofer` sidecar binary -- did you copy it to \
                         src-tauri/binaries/gofer-<target-triple>? See tauri-wrapper/README.md.",
                );

                let (_rx, child) = sidecar_command
                    .current_dir(gofer_working_dir(&app_handle))
                    .envs(SIDECAR_ENV)
                    .spawn()
                    .expect("failed to spawn the Gofer sidecar process");

                let state = app_handle.state::<SidecarState>();
                *state.0.lock().unwrap() = Some(child);
            }

            // Poll for the server on a background thread so setup() returns
            // immediately and the window can appear right away with the
            // placeholder page. WebviewWindow::navigate() is safe to call
            // off the main thread -- Tauri proxies it onto the event loop.
            let app_handle_for_poll = app_handle.clone();
            std::thread::spawn(move || {
                if !wait_for_port(STARTUP_TIMEOUT) {
                    eprintln!(
                        "Gofer did not start listening on {GOFER_HOST}:{GOFER_PORT} within \
                         {STARTUP_TIMEOUT:?}. Leaving the loading screen up. Check that \
                         src-tauri/binaries/gofer-<target-triple> exists and runs standalone \
                         (try running it directly from a terminal to see its own errors)."
                    );
                    return;
                }

                if let Some(window) = app_handle_for_poll.get_webview_window("main") {
                    if let Err(err) = window.navigate(GOFER_URL.parse().expect("valid URL")) {
                        eprintln!("failed to navigate main window to Gofer: {err}");
                    }
                } else {
                    eprintln!("main window not found; could not navigate to Gofer");
                }
            });

            Ok(())
        })
        // Belt-and-suspenders: kill the sidecar as soon as the window is
        // asked to close, in addition to the RunEvent::Exit handler below.
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { .. } = event {
                kill_sidecar(window.app_handle());
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building the Tauri application")
        .run(|app_handle, event| {
            if let RunEvent::Exit = event {
                kill_sidecar(app_handle);
            }
        });
}
