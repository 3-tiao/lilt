{
  description = "lilt — terminal client for Apple Music, Audius, Jamendo, and web radio";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      lib = nixpkgs.lib;
      linuxSystems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forLinux = f: lib.genAttrs linuxSystems (system: f nixpkgs.legacyPackages.${system});

      # Shell packages shared by every dev shell. mpv is the Linux playback
      # backend; macOS uses the signed helpers.
      shellPackages = pkgs: [
        pkgs.go
        pkgs.just
        pkgs.zsh
        pkgs.sqlite
      ] ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.mpv ];

      # Widevine is proprietary, so nixpkgs ships it as a wrapper around an
      # otherwise unmodified Chromium.
      widevineChromium = pkgs: pkgs.chromium.override { enableWideVine = true; };

      # Only the shared Go layer is packaged. On macOS the shipped product also
      # contains two signed Swift helpers, which need Xcode and a Developer ID —
      # build those with `just build` instead of Nix. The Linux package is the
      # `lilt` binary alone; playback needs `mpv` on PATH at runtime.
      version = self.shortRev or self.dirtyShortRev or "unknown";
    in
    {
      packages = forLinux (pkgs:
        let
          liltUnwrapped = pkgs.buildGoModule {
          pname = "lilt";
          inherit version;
          src = self;
          subPackages = [ "cmd/lilt" ];
          ldflags = [ "-X main.version=${version}" ];
          vendorHash = "sha256-xwAA56iy3vKjIT2bNot2TSjM/d2+RfK+NbP7DYyt9Ac=";
          meta = {
            description = "Terminal client for Apple Music, Audius, Jamendo, and web radio";
            homepage = "https://github.com/3-tiao/lilt";
            license = lib.licenses.mit;
            mainProgram = "lilt";
            platforms = lib.platforms.linux;
          };
          };
          mkLilt = { apple ? false }:
            pkgs.symlinkJoin {
              name = if apple then "lilt-apple-${version}" else "lilt-${version}";
              paths = [ liltUnwrapped ]
                ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.mpv ]
                ++ lib.optionals apple [ (widevineChromium pkgs) ];
              nativeBuildInputs = [ pkgs.makeWrapper ];
              postBuild = ''
                wrapProgram $out/bin/lilt \
                  --prefix PATH : ${lib.makeBinPath (lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.mpv ])}
                  ${lib.optionalString apple "--set LILT_CHROMIUM_PATH ${widevineChromium pkgs}/bin/chromium"}
              '';
            };
          lilt = mkLilt { };
        in
        {
          inherit lilt;
          default = lilt;
        } // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          "lilt-apple" = mkLilt { apple = true; };
        });

      devShells = forLinux (pkgs: {
        default = pkgs.mkShell {
          packages = shellPackages pkgs;
        };
        # `nix develop .#apple` also provides a Chromium with Widevine, which is
        # what the Linux Apple Music engine drives. It is a separate shell because
        # the CDM is unfree, so a default shell would silently impose that licence
        # on everyone; evaluate it with NIXPKGS_ALLOW_UNFREE=1.
        apple = pkgs.mkShell {
          packages = shellPackages pkgs
            ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [ (widevineChromium pkgs) ];
          shellHook = lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
            export LILT_CHROMIUM_PATH=${widevineChromium pkgs}/bin/chromium
          '';
        };
      });
    };
}
