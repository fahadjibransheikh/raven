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
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

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
/// "Starting Gofer..." placeholder on screen.
const STARTUP_TIMEOUT: Duration = Duration::from_secs(15);
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
            let quit_item = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
            let tray_menu = Menu::with_items(
                app,
                &[
                    &show_item,
                    &compose_item,
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
                    "quit" => app.exit(0),
                    _ => {}
                })
                .build(app)?;

            // If Gofer is already listening (previous run, or started by
            // hand), don't spawn a second copy -- just adopt it.
            if !port_is_open() {
                let sidecar_command = app_handle.shell().sidecar("gofer").expect(
                    "failed to resolve the `gofer` sidecar binary -- did you copy it to \
                         src-tauri/binaries/gofer-<target-triple>? See tauri-wrapper/README.md.",
                );

                let (_rx, child) = sidecar_command
                    .current_dir(gofer_working_dir(&app_handle))
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
