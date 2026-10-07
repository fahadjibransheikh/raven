//! Gofer desktop wrapper.
//!
//! This crate is deliberately NOT a UI. It exists only to:
//!   1. Launch the compiled Gofer Go binary (bundled as a Tauri "sidecar",
//!      see https://v2.tauri.app/develop/sidecar/) as a background process.
//!   2. Wait for it to start listening on 127.0.0.1:<port> (8090 unless something
//!      else holds it), for as long as the process is alive (migrations can take minutes); if it exits first,
//!      show its last output and a "Try again" button instead.
//!   3. Point the one native window at http://127.0.0.1:<port>, so the user
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

use std::collections::VecDeque;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::sync::atomic::{AtomicBool, AtomicU16, AtomicU64, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Manager, RunEvent, WindowEvent};
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;
use tauri_plugin_updater::UpdaterExt;

/// Injected into the main window; polls Gofer's unread count and reflects it
/// in `document.title` as a "(N) " prefix, which `on_document_title_changed`
/// below parses back out into the Dock/taskbar badge.
const UNREAD_BADGE_SCRIPT: &str = include_str!("unread_badge.js");

/// The port Gofer is started on when it is free. The Microsoft app's redirect
/// URIs are registered for http://localhost:8090, so every other port breaks
/// the "Add Outlook account" sign-in (see pick_port).
const PREFERRED_PORT: u16 = 8090;

/// Settings the desktop app always runs Gofer with, so Outlook works on every
/// install without editing a .env. The client ID is Raven's Azure app,
/// registered as a public client (no secret; PKCE protects the code exchange),
/// so it is safe to ship. Gofer's .env loader never overrides variables that
/// are already set, so these win over any stale values in a user's .env.
/// GOFER_ADDR and GOFER_BASE_URL are added per launch, from the chosen port.
const SIDECAR_ENV: [(&str, &str); 3] = [
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
fn gofer_working_dir(app: &AppHandle) -> Result<std::path::PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|err| format!("Could not locate the Raven data folder: {err}"))?;
    std::fs::create_dir_all(&dir)
        .map_err(|err| format!("Could not create the Raven data folder {}: {err}", dir.display()))?;
    Ok(dir)
}

/// How long the placeholder says only "Starting Raven..." before switching to
/// the longer "Updating your mailbox" text. A normal launch is well under this.
const SLOW_START_AFTER: Duration = Duration::from_secs(5);
const POLL_INTERVAL: Duration = Duration::from_millis(200);
/// How many of the sidecar's last output lines the error page shows.
const TAIL_LINES: usize = 12;

/// What the placeholder page (frontend/status.js) should currently show.
#[derive(Clone, serde::Serialize)]
struct StartupView {
    /// "starting", "slow" or "error".
    state: &'static str,
    /// One line of progress (slow) or the failure reason (error).
    detail: String,
    /// The sidecar's last output lines, error state only.
    tail: String,
}

impl StartupView {
    fn starting() -> Self {
        Self { state: "starting", detail: String::new(), tail: String::new() }
    }
}

/// Managed app state for the sidecar this app instance spawned. `child` is
/// `None` before the first spawn and after a kill.
struct SidecarState {
    child: Mutex<Option<CommandChild>>,
    /// The port the current sidecar was told to listen on.
    port: AtomicU16,
    /// Bumped on every (re)start so a superseded startup thread stops itself.
    attempt: AtomicU64,
    /// Set once the window has been pointed at Gofer; "Try again" is a no-op after.
    ready: AtomicBool,
    /// Why the sidecar process ended, if it did.
    exit: Mutex<Option<String>>,
    /// The sidecar's most recent output lines.
    tail: Mutex<VecDeque<String>>,
    /// Latest migration/boot progress line from the sidecar's log.
    progress: Mutex<String>,
    view: Mutex<StartupView>,
    /// Random per-launch secret. Passed to the sidecar as GOFER_DESKTOP_TOKEN
    /// and traded for a cookie at /desktop-auth, so only this app's webview can
    /// use the server. Never written to disk or logged.
    token: String,
}

impl SidecarState {
    fn new() -> Self {
        Self {
            child: Mutex::new(None),
            port: AtomicU16::new(PREFERRED_PORT),
            attempt: AtomicU64::new(0),
            ready: AtomicBool::new(false),
            exit: Mutex::new(None),
            tail: Mutex::new(VecDeque::new()),
            progress: Mutex::new(String::new()),
            view: Mutex::new(StartupView::starting()),
            token: new_token(),
        }
    }
}

fn new_token() -> String {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes).expect("the OS random number generator is unavailable");
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// True once the server on `port` answers as Gofer's desktop gate: an
/// unauthenticated GET /desktop-auth gets 403 with an X-Raven-Desktop header.
/// Checked before the token is ever sent, so a listener that merely accepts
/// TCP connections is not enough to receive it.
fn gofer_answers(port: u16) -> bool {
    let addr = std::net::SocketAddr::from(([127, 0, 0, 1], port));
    let Ok(mut stream) = TcpStream::connect_timeout(&addr, Duration::from_millis(500)) else {
        return false;
    };
    let timeout = Some(Duration::from_secs(2));
    let _ = stream.set_read_timeout(timeout);
    let _ = stream.set_write_timeout(timeout);
    let request =
        format!("GET /desktop-auth HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nConnection: close\r\n\r\n");
    if stream.write_all(request.as_bytes()).is_err() {
        return false;
    }
    let mut response = String::new();
    // Headers are all that is needed; a read error after them is fine.
    let _ = stream.take(8192).read_to_string(&mut response);
    let mut lines = response.lines();
    lines.next().is_some_and(|status| status.contains(" 403"))
        && lines.any(|line| line.eq_ignore_ascii_case("x-raven-desktop: 1"))
}

/// The URL that logs the webview in and lands on `next` (a same-origin path).
fn login_url(port: u16, token: &str, next: &str) -> tauri::Url {
    let mut url: tauri::Url = format!("http://127.0.0.1:{port}/desktop-auth")
        .parse()
        .expect("valid URL");
    url.query_pairs_mut().append_pair("t", token).append_pair("next", next);
    url
}

/// Picks the port for this launch: 8090 if it can be bound, otherwise any free
/// port. OAuth implication of the fallback: GOFER_BASE_URL (and so the
/// redirect URI sent to Microsoft/Google) follows the port, and redirect URIs
/// registered for localhost:8090 will not match, so adding a mailbox through
/// OAuth can fail until 8090 is free again. Everything else keeps working.
/// The port is released before the sidecar binds it; if someone grabs it in
/// that gap the sidecar exits with a bind error, which the error page shows.
fn pick_port() -> u16 {
    use std::net::TcpListener;
    for candidate in [PREFERRED_PORT, 0] {
        if let Ok(listener) = TcpListener::bind(("127.0.0.1", candidate)) {
            if let Ok(addr) = listener.local_addr() {
                return addr.port();
            }
        }
    }
    PREFERRED_PORT
}

fn kill_sidecar(app_handle: &tauri::AppHandle) {
    let state = app_handle.state::<SidecarState>();
    let child = state.child.lock().unwrap().take();
    if let Some(child) = child {
        // Best-effort: if the process already exited, this can fail; that's
        // fine, there's nothing to clean up.
        let _ = child.kill();
    }
}

/// Records the view and pushes it to the placeholder page. The page also pulls
/// it with `startup_state` on load, so a push that lands before the page has
/// loaded (and is dropped) is not lost.
fn set_view(app: &AppHandle, view: StartupView) {
    *app.state::<SidecarState>().view.lock().unwrap() = view.clone();
    if let Some(window) = app.get_webview_window("main") {
        if let Ok(json) = serde_json::to_string(&view) {
            let _ = window.eval(format!("window.ravenStatus&&window.ravenStatus.render({json})"));
        }
    }
}

#[tauri::command]
fn startup_state(state: tauri::State<'_, SidecarState>) -> StartupView {
    state.view.lock().unwrap().clone()
}

/// The error page's "Try again" button. Ignored once Gofer is up, so the
/// remote Raven page (which can also reach app commands) cannot restart it.
#[tauri::command]
fn retry_startup(app: AppHandle) {
    if !app.state::<SidecarState>().ready.load(Ordering::SeqCst) {
        start_sidecar(&app);
    }
}

fn push_output(state: &SidecarState, bytes: &[u8]) {
    let line = String::from_utf8_lossy(bytes).trim().to_string();
    if line.is_empty() {
        return;
    }
    // Go's log lines look like "2026/10/06 12:00:00 storage: vacuuming (...)":
    // keep the part from the subsystem prefix on as user-facing progress.
    if let Some(at) = ["storage:", "boot:"].iter().filter_map(|p| line.find(p)).min() {
        *state.progress.lock().unwrap() = line[at..].to_string();
    }
    let mut tail = state.tail.lock().unwrap();
    if tail.len() == TAIL_LINES {
        tail.pop_front();
    }
    tail.push_back(line);
}

/// (Re)starts Gofer and the background thread that waits for it. Safe to call
/// again after a failure: it supersedes any earlier attempt.
fn start_sidecar(app: &AppHandle) {
    let state = app.state::<SidecarState>();
    let attempt = state.attempt.fetch_add(1, Ordering::SeqCst) + 1;
    kill_sidecar(app);
    state.ready.store(false, Ordering::SeqCst);
    *state.exit.lock().unwrap() = None;
    state.tail.lock().unwrap().clear();
    state.progress.lock().unwrap().clear();
    set_view(app, StartupView::starting());

    let fail = |detail: String| {
        let tail = app.state::<SidecarState>().tail.lock().unwrap().iter().cloned().collect::<Vec<_>>().join("\n");
        set_view(app, StartupView { state: "error", detail, tail });
    };

    // Never adopt a server that is already listening: it cannot be told apart
    // from an impostor, and a Raven started elsewhere does not know this
    // launch's secret. If 8090 is taken we simply use another port.
    let port = pick_port();
    state.port.store(port, Ordering::SeqCst);
    {
        let command = match app.shell().sidecar("gofer") {
            Ok(command) => command,
            Err(err) => {
                fail(format!(
                    "The bundled server could not be found ({err}). Reinstall Raven, or see \
                     tauri-wrapper/README.md to stage the sidecar binary."
                ));
                return;
            }
        };
        let working_dir = match gofer_working_dir(app) {
            Ok(dir) => dir,
            Err(err) => {
                fail(err);
                return;
            }
        };
        let (mut rx, child) = match command
            .current_dir(working_dir)
            .envs(SIDECAR_ENV)
            .env("GOFER_ADDR", format!("127.0.0.1:{port}"))
            .env("GOFER_BASE_URL", format!("http://localhost:{port}"))
            .env("GOFER_DESKTOP_TOKEN", &state.token)
            .spawn() {
            Ok(spawned) => spawned,
            Err(err) => {
                fail(format!("The server could not be started: {err}"));
                return;
            }
        };
        *state.child.lock().unwrap() = Some(child);

        // Drain the sidecar's output (a full pipe would stall it) and note when
        // it exits. This is also the only way to learn it died before listening.
        let app_for_events = app.clone();
        tauri::async_runtime::spawn(async move {
            while let Some(event) = rx.recv().await {
                let state = app_for_events.state::<SidecarState>();
                if state.attempt.load(Ordering::SeqCst) != attempt {
                    return;
                }
                match event {
                    CommandEvent::Stdout(bytes) | CommandEvent::Stderr(bytes) => {
                        push_output(&state, &bytes)
                    }
                    CommandEvent::Error(err) => push_output(&state, err.as_bytes()),
                    CommandEvent::Terminated(payload) => {
                        let how = match (payload.code, payload.signal) {
                            (Some(code), _) => format!("exited with code {code}"),
                            (None, Some(signal)) => format!("was stopped by signal {signal}"),
                            _ => "stopped".to_string(),
                        };
                        *state.exit.lock().unwrap() = Some(format!("The server {how} before it started."));
                        return;
                    }
                    _ => {}
                }
            }
        });
    }

    // Wait on a background thread so setup() returns immediately and the window
    // can show the placeholder. WebviewWindow::navigate() is safe off the main
    // thread -- Tauri proxies it onto the event loop.
    let app = app.clone();
    std::thread::spawn(move || {
        let state = app.state::<SidecarState>();
        let started = Instant::now();
        let mut shown_progress = String::new();
        let mut slow = false;
        loop {
            if state.attempt.load(Ordering::SeqCst) != attempt {
                return;
            }
            // Process death is checked before the port so nothing else that
            // happens to be listening can be mistaken for Gofer.
            if let Some(reason) = state.exit.lock().unwrap().clone() {
                // Give the output reader a moment to deliver the last lines.
                std::thread::sleep(Duration::from_millis(300));
                let tail = state.tail.lock().unwrap().iter().cloned().collect::<Vec<_>>().join("\n");
                set_view(&app, StartupView { state: "error", detail: reason, tail });
                return;
            }
            if gofer_answers(port) {
                break;
            }
            if started.elapsed() >= SLOW_START_AFTER {
                let progress = state.progress.lock().unwrap().clone();
                if !slow || progress != shown_progress {
                    slow = true;
                    shown_progress = progress.clone();
                    set_view(&app, StartupView { state: "slow", detail: progress, tail: String::new() });
                }
            }
            std::thread::sleep(POLL_INTERVAL);
        }

        state.ready.store(true, Ordering::SeqCst);
        if let Some(window) = app.get_webview_window("main") {
            if let Err(err) = window.navigate(login_url(port, &state.token, "/")) {
                eprintln!("failed to navigate main window to Gofer: {err}");
            }
        } else {
            eprintln!("main window not found; could not navigate to Gofer");
        }
    });
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
        .manage(SidecarState::new())
        .invoke_handler(tauri::generate_handler![startup_state, retry_startup])
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
                .on_navigation({
                    let app_handle = app_handle.clone();
                    move |url| {
                        // GOFER_BASE_URL says localhost (that is what the OAuth
                        // redirect URIs are registered for), so a mailbox
                        // sign-in comes back to localhost:<port>. The desktop
                        // cookie belongs to 127.0.0.1, so replay that
                        // navigation there instead of landing cookieless.
                        let port = app_handle.state::<SidecarState>().port.load(Ordering::SeqCst);
                        if url.scheme() == "http"
                            && url.host_str() == Some("localhost")
                            && url.port() == Some(port)
                        {
                            let mut local = url.clone();
                            if local.set_host(Some("127.0.0.1")).is_ok() {
                                let app_handle = app_handle.clone();
                                std::thread::spawn(move || {
                                    if let Some(window) = app_handle.get_webview_window("main") {
                                        let _ = window.navigate(local);
                                    }
                                });
                                return false;
                            }
                        }
                        true
                    }
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

            start_sidecar(&app_handle);

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
