fn main() {
    // Declaring the app's commands turns on Tauri's ACL for them. That is what
    // lets the remote Raven page (http://127.0.0.1:<port>) invoke set_app_icon,
    // and it means the local startup page's two commands need permissions too
    // (capabilities/default.json).
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&["startup_state", "retry_startup", "set_app_icon"]),
    ))
    .expect("failed to run tauri-build");
}
