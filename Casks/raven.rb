cask "raven" do
  version "0.1.4"
  sha256 "aa0a9345d6bd48365a0b3a246925957a89f90c15005cd28ba59de3d1e1498a79"

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
