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

use tauri::{Manager, RunEvent, WindowEvent};
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

/// Host/port Gofer listens on. This matches `GOFER_ADDR=127.0.0.1:8090` in
/// the repo's `.env` (see gofer/.env / gofer/.env.example). If you change
/// GOFER_ADDR, update these two constants to match.
const GOFER_HOST: &str = "127.0.0.1";
const GOFER_PORT: u16 = 8090;
const GOFER_URL: &str = "http://127.0.0.1:8090";

/// Working directory the sidecar is spawned with. Gofer reads its .env
/// (OAuth credentials) and its data/ SQLite store relative to its process
/// working directory, so this must point at the real repo root -- the same
/// place `.env` and `data/` already live from running `task build`/
/// `./tmp/main` by hand. Update this if the repo is ever moved again.
const GOFER_WORKING_DIR: &str = "/Users/fahad/code/raven/gofer";

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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(SidecarState(Arc::new(Mutex::new(None))))
        .setup(|app| {
            let app_handle = app.handle().clone();

            // If Gofer is already listening (previous run, or started by
            // hand), don't spawn a second copy -- just adopt it.
            if !port_is_open() {
                let sidecar_command = app_handle.shell().sidecar("gofer").expect(
                    "failed to resolve the `gofer` sidecar binary -- did you copy it to \
                         src-tauri/binaries/gofer-<target-triple>? See tauri-wrapper/README.md.",
                );

                let (_rx, child) = sidecar_command
                    .current_dir(GOFER_WORKING_DIR)
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
