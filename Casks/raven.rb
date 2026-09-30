cask "raven" do
  version "0.1.2"
  sha256 "606904ffdbbf5d7d50888a1ffc427b1554a87553b9d6942b164a97d535251cc2"

  url "https://github.com/fahadjibransheikh/raven/releases/download/v#{version}/Raven_#{version}_aarch64.dmg"
  name "Raven"
  desc "Desktop email client"
  homepage "https://github.com/fahadjibransheikh/raven"

  livecheck do
    url :url
    strategy :github_latest
  end

  auto_updates true
  depends_on arch: :arm64

  app "Raven.app"

  # The app is not signed or notarized yet, so Gatekeeper would refuse to open
  # it. Remove this postflight once it is.
  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/Raven.app"]
  end

  zap trash: [
    "~/Library/Application Support/com.fahadsheikh.raven",
    "~/Library/Caches/com.fahadsheikh.raven",
    "~/Library/Saved Application State/com.fahadsheikh.raven.savedState",
    "~/Library/WebKit/com.fahadsheikh.raven",
  ]
end
