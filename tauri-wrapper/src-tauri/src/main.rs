// Prevents an additional console window from appearing on Windows in release
// builds. Irrelevant on macOS but harmless to keep -- DO NOT REMOVE.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    gofer_desktop_lib::run();
}
