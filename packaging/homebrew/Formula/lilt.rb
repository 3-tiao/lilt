# Homebrew formula for lilt v1.0.0.
#
# The published copy lives in `3-tiao/homebrew-lilt/Formula/lilt.rb`.
# Update version/url/sha256 together for future releases.
# See docs/product/release.md for the full process.
class Lilt < Formula
  desc "Apple Music, Audius, Jamendo, and internet-radio terminal controller"
  homepage "https://github.com/3-tiao/lilt"
  url "https://github.com/3-tiao/lilt/releases/download/v1.0.0/lilt-v1.0.0-darwin-arm64.tar.gz"
  sha256 "74ab1658955f1c3c1cdfc3d3f7f01c19a6c00d740c4ff897df896521a8bf7bfe"
  license "MIT"

  depends_on arch: :arm64
  # The signed helper uses MusicKit, which requires macOS 14+.
  depends_on macos: :sonoma

  # Both signed helper apps embed @rpath frameworks. Rewriting their IDs
  # invalidates the Developer ID signatures and stapled notarization tickets.
  preserve_rpath

  def install
    # Keep the binary and both signed helper apps together, then expose a
    # wrapper that points the CLI at the bundled apps.
    libexec.install "lilt"
    libexec.install "lilt-player.app"
    libexec.install "lilt-audio.app"
    (bin/"lilt").write_env_script libexec/"lilt",
                                  LILT_PLAYER_PATH: libexec/"lilt-player.app",
                                  LILT_AUDIO_PATH:  libexec/"lilt-audio.app"
    # The agent skill ships with the product; the caveats below show how to make
    # a harness see it (a formula must not write into user dotfiles itself).
    pkgshare.install "skills/music-control"
  end

  def caveats
    <<~EOS
      The agent skill is installed at:
        #{opt_pkgshare}/music-control

      Link it into your agent's skills directory, for example:
        mkdir -p ~/.agents/skills && ln -sfn #{opt_pkgshare}/music-control ~/.agents/skills/music-control          # pi
        mkdir -p ~/.config/opencode/skills && ln -sfn #{opt_pkgshare}/music-control ~/.config/opencode/skills/music-control   # opencode
    EOS
  end

  test do
    assert_match "lilt #{version}", shell_output("#{bin}/lilt version")
    # spctl runs outside brew test's sandbox during release/install acceptance.
    %w[lilt-player lilt-audio].each do |helper|
      app = libexec/"#{helper}.app"
      system "codesign", "--verify", "--strict", app
      system "xcrun", "stapler", "validate", app
    end
  end
end
