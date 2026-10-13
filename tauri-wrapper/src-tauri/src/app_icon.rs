//! The app icon picker's native half. The Raven page calls `set_app_icon` with
//! the chosen style and whether Raven is showing its dark theme (ui-settings.js,
//! applyAppIcon). Raven has its own light/dark setting and window_chrome.js
//! forces the window theme to match it, so the page, not the OS, is the source
//! of truth for the appearance; there is nothing to re-apply on a system theme
//! change.
//!
//! macOS changes the running Dock icon. Windows and Linux set the window icon.
//! Finder, Launchpad and the installed bundle keep the icon from
//! tauri.conf.json on purpose, and nothing is persisted: the page re-sends the
//! choice on every load.

use tauri::AppHandle;

// Same table as appIconStyles in internal/views/helpers.go and APP_ICONS in
// assets/js/ui-settings.js: [dark, light] per style.
const RAVEN_DARK: &[u8] = include_bytes!("../../../assets/brand/logo-raven.png");
const RAVEN_LIGHT: &[u8] = include_bytes!("../../../assets/brand/logo-raven-light.png");
const CLASSIC_DARK: &[u8] = include_bytes!("../../../assets/brand/logo-classic.png");
const CLASSIC_LIGHT: &[u8] = include_bytes!("../../../assets/brand/logo-classic-light.png");
const MINIMAL_DARK: &[u8] = include_bytes!("../../../assets/brand/logo-minimal-dark.png");
const MINIMAL_LIGHT: &[u8] = include_bytes!("../../../assets/brand/logo-minimal-light.png");

fn icon_png(style: &str, dark: bool) -> Option<&'static [u8]> {
    Some(match (style, dark) {
        ("raven", true) => RAVEN_DARK,
        ("raven", false) => RAVEN_LIGHT,
        ("classic", true) => CLASSIC_DARK,
        ("classic", false) => CLASSIC_LIGHT,
        ("minimal", true) => MINIMAL_DARK,
        ("minimal", false) => MINIMAL_LIGHT,
        _ => return None,
    })
}

#[tauri::command]
pub fn set_app_icon(app: AppHandle, style: String, dark: bool) -> Result<(), String> {
    let png = icon_png(&style, dark).ok_or_else(|| format!("unknown app icon style {style:?}"))?;
    apply(&app, png)
}

#[cfg(target_os = "macos")]
fn apply(app: &AppHandle, png: &'static [u8]) -> Result<(), String> {
    use objc2::{AllocAnyThread, MainThreadMarker};
    use objc2_app_kit::{NSApplication, NSImage};
    use objc2_foundation::NSData;

    // AppKit objects may only be touched on the main thread; commands run elsewhere.
    app.run_on_main_thread(move || {
        let Some(mtm) = MainThreadMarker::new() else {
            return;
        };
        let data = NSData::with_bytes(png);
        let Some(image) = NSImage::initWithData(NSImage::alloc(), &data) else {
            eprintln!("app icon: PNG could not be decoded");
            return;
        };
        // SAFETY: called on the main thread with a live NSImage; the setter
        // retains it.
        unsafe { NSApplication::sharedApplication(mtm).setApplicationIconImage(Some(&image)) };
    })
    .map_err(|err| err.to_string())
}

#[cfg(not(target_os = "macos"))]
fn apply(app: &AppHandle, png: &'static [u8]) -> Result<(), String> {
    set_window_icons(app, png)
}

// Not cfg-gated so macOS builds type-check it too; only the other platforms call it.
#[cfg_attr(target_os = "macos", allow(dead_code))]
fn set_window_icons(app: &AppHandle, png: &[u8]) -> Result<(), String> {
    use tauri::Manager;
    let image = tauri::image::Image::from_bytes(png).map_err(|err| err.to_string())?;
    for window in app.webview_windows().values() {
        window.set_icon(image.clone()).map_err(|err| err.to_string())?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_style_and_appearance_has_a_distinct_png() {
        let mut seen = Vec::new();
        for style in ["raven", "classic", "minimal"] {
            for dark in [true, false] {
                let png = icon_png(style, dark).expect("known style");
                assert_eq!(&png[1..4], b"PNG", "{style} dark={dark} is not a PNG");
                assert!(!seen.contains(&png), "{style} dark={dark} repeats another icon");
                seen.push(png);
            }
        }
    }

    #[test]
    fn unknown_styles_are_rejected() {
        for style in ["", "Raven", "express", "../logo"] {
            assert!(icon_png(style, true).is_none(), "{style:?}");
        }
    }
}
