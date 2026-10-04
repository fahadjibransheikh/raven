cask "raven" do
  version "0.1.7"
  sha256 arm:          "7229b4375205246caf694a208b81761ed550d452740bfdfcf425df6db02746d5",
         x86_64_linux: "51b2dacefd9eeaed74887da052660048fbafafd30e3b6bb43a29be38d81138d6"

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
