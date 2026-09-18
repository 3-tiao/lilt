# Homebrew formula template for lilt.
#
# Copy this file into the tap repo `Older-Youth-HZ/homebrew-lilt` as
# `Formula/lilt.rb`, then fill version/url/sha256 from `just release`.
# See docs/product/release.md for the full process.
class Lilt < Formula
  desc "Apple Music, Audius, and internet-radio terminal controller"
  homepage "https://github.com/Older-Youth-HZ/lilt"
  version "0.1.0"
  url "https://github.com/Older-Youth-HZ/lilt/releases/download/v#{version}/lilt-v#{version}-darwin-arm64.tar.gz"
  sha256 "REPLACE_WITH_SHA256_FROM_JUST_RELEASE"
  license "MIT"

  depends_on :macos
  # The signed helper uses MusicKit, which requires macOS 14+.
  depends_on macos: :sonoma

  def install
    # Keep the binary and both signed helper apps together, then expose a
    # wrapper that points the CLI at the bundled apps.
    libexec.install "lilt"
    libexec.install "lilt-player.app"
    libexec.install "lilt-audio.app"
    (bin/"lilt").write_env_script libexec/"lilt",
                                  LILT_PLAYER_PATH: libexec/"lilt-player.app",
                                  LILT_AUDIO_PATH: libexec/"lilt-audio.app"
  end

  test do
    assert_match "lilt #{version}", shell_output("#{bin}/lilt version")
  end
end
