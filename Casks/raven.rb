cask "raven" do
  version "0.3.4"
  sha256 arm:          "a93f06be53081ba8351e6dff2de2e94ea26790c9e35f5348c1ec961c1036bcc9",
         x86_64_linux: "6330e9e43b2ffb6a8871879a5a3e5d2672b5f7bdd877cedbbb7e1b345d913ac9"

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
