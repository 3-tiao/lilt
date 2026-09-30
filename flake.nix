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
      linuxPkgs = system: import nixpkgs { inherit system; };
      # An external NixOS configuration cannot propagate its nixpkgs config into
      # this flake's package outputs. Keep the default packages on normal pkgs,
      # and use this explicitly selected package set only for Widevine outputs.
      linuxUnfreePkgs = system: import nixpkgs {
        inherit system;
        config.allowUnfree = true;
      };
      forLinux = f: lib.genAttrs linuxSystems (system: f {
        pkgs = linuxPkgs system;
        unfreePkgs = linuxUnfreePkgs system;
      });

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
      packages = forLinux ({ pkgs, unfreePkgs }:
        let
          mkLiltUnwrapped = packagePkgs: packagePkgs.buildGoModule {
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
          mkLilt = packagePkgs: { apple ? false }:
            packagePkgs.symlinkJoin {
              name = if apple then "lilt-apple-${version}" else "lilt-${version}";
              paths = [ (mkLiltUnwrapped packagePkgs) packagePkgs.mpv ]
                ++ lib.optionals apple [ (widevineChromium packagePkgs) ];
              nativeBuildInputs = [ packagePkgs.makeWrapper ];
              postBuild = ''
                wrapProgram $out/bin/lilt \
                  --prefix PATH : ${lib.makeBinPath [ packagePkgs.mpv ]}
                  ${lib.optionalString apple "--set LILT_CHROMIUM_PATH ${widevineChromium packagePkgs}/bin/chromium"}
              '';
            };
          lilt = mkLilt pkgs { };
        in
        {
          inherit lilt;
          default = lilt;
        } // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          "lilt-apple" = mkLilt unfreePkgs { apple = true; };
        });

      devShells = forLinux ({ pkgs, unfreePkgs }: {
        default = pkgs.mkShell {
          packages = shellPackages pkgs;
        };
        # `nix develop .#apple` also provides a Chromium with Widevine, which is
        # what the Linux Apple Music engine drives. It is a separate shell because
        # the CDM is unfree, so a default shell would silently impose that licence
        # on everyone. Selecting this shell is the explicit opt-in.
        apple = unfreePkgs.mkShell {
          packages = shellPackages unfreePkgs ++ [ (widevineChromium unfreePkgs) ];
          shellHook = ''
            export LILT_CHROMIUM_PATH=${widevineChromium unfreePkgs}/bin/chromium
          '';
        };
      });
    };
}
