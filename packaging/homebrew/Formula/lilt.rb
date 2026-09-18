# Homebrew formula template for lilt.
#
# Copy this file into the tap repo `Older-Youth-HZ/homebrew-tap` as
# `Formula/lilt.rb`, then fill version/url/sha256 from `just release`.
# See docs/product/release.md for the full process.
class Lilt < Formula
  desc "Apple Music, Audius, and internet-radio terminal controller"
  homepage "https://github.com/Older-Youth-HZ/lilt"
  version "0.1.0"
  url "https://github.com/Older-Youth-HZ/lilt/releases/download/v#{version}/lilt-v#{version}-darwin-arm64.tar.gz"
  sha256 "REPLACE_WITH_SHA256_FROM_JUST_RELEASE"

  depends_on :macos
  # The signed helper uses MusicKit, which requires macOS 14+.
  depends_on macos: :sonoma

  def install
    # Keep the real binary and the signed helper app together, then expose a
    # wrapper that points the CLI at the bundled app.
    libexec.install "lilt"
    libexec.install "lilt-player.app"
    (bin/"lilt").write_env_script libexec/"lilt",
                                  LILT_PLAYER_PATH: libexec/"lilt-player.app"
  end

  test do
    assert_match "lilt #{version}", shell_output("#{bin}/lilt version")
  end
end
