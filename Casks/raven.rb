cask "raven" do
  version "1.2.1"
  sha256 arm:          "8c5e4935b9084bd6b5ef6d681d7af3eeb14c211a01aa3498e39cfe409a141d98",
         x86_64_linux: "d2d90f40a42487f7e71eee49219dffc8633ced05645f1674dbe179bc63e808ff"

  on_macos do
    url "https://github.com/fahadjibransheikh/raven/releases/download/v#{version}/Raven_#{version}_aarch64.dmg"

    depends_on arch: :arm64

    app "Raven.app"

    # The app is not signed or notarized yet, so Gatekeeper would refuse to open
    # it. Remove these postflight steps once it is.
    postflight_steps do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{appdir}}/Raven.app"]
    end

    zap trash: [
      "~/Library/Application Support/com.fahadsheikh.raven",
      "~/Library/Caches/com.fahadsheikh.raven",
      "~/Library/Saved Application State/com.fahadsheikh.raven.savedState",
      "~/Library/WebKit/com.fahadsheikh.raven",
    ]
  end
  on_linux do
    url "https://github.com/fahadjibransheikh/raven/releases/download/v#{version}/Raven_#{version}_amd64.AppImage"

    depends_on arch: :x86_64

    # Version-less target, so the in-app updater replaces the file in place.
    app_image "Raven_#{version}_amd64.AppImage", target: "Raven.AppImage"

    # Homebrew only places the AppImage, so add an app launcher entry. Install
    # steps run with a sandboxed $HOME, so home paths use `base: :home`, and
    # Exec uses ~ for sh to expand when the launcher starts Raven.
    postflight_steps do
      run "/usr/bin/curl",
          args:           ["-fsSL", "https://raw.githubusercontent.com/fahadjibransheikh/raven/v{{version}}/tauri-wrapper/src-tauri/icons/icon.png"],
          stdout_path:    "raven.png",
          network_access: true
      copy "raven.png", ".local/share/icons/hicolor/512x512/apps/raven.png", target_base: :home
      write_file ".local/share/applications/raven.desktop", <<~DESKTOP, base: :home
        [Desktop Entry]
        Type=Application
        Name=Raven
        Comment=Desktop email client
        Exec=sh -c "exec ~/Applications/Raven.AppImage"
        Icon=raven
        Terminal=false
        Categories=Network;Email;
      DESKTOP
    end

    uninstall_postflight_steps do
      remove [".local/share/applications/raven.desktop",
              ".local/share/icons/hicolor/512x512/apps/raven.png"],
             base: :home
    end

    zap trash: "~/.local/share/com.fahadsheikh.raven"
  end

  name "Raven"
  desc "Desktop email client"
  homepage "https://github.com/fahadjibransheikh/raven"

  livecheck do
    url :url
    strategy :github_latest
  end

  auto_updates true
end
