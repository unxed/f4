{
  description = "f4 - efficient and cozy file manager in Go (Far Manager / far2l-style UX)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  # Consumed by checks only: homeManagerModules below is a plain module and
  # takes its Home Manager from whichever configuration imports it.
  inputs.home-manager.url = "github:nix-community/home-manager";
  inputs.home-manager.inputs.nixpkgs.follows = "nixpkgs";

  outputs = { self, nixpkgs, home-manager }:
    let
      inherit (nixpkgs) lib;
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: lib.genAttrs systems (system: f {
        pkgs = nixpkgs.legacyPackages.${system};
        inherit system;
      });
      version = if self ? rev then "unstable-${self.shortRev}" else "dirty";
    in
    {
      packages = forAllSystems ({ pkgs, ... }: let
        mkF4 = ttyOnly: pkgs.buildGoModule {
          pname = if ttyOnly then "f4-tty" else "f4-gui";
          inherit version;
          src = self;

          # f4 builds with CGO_ENABLED=0 and reaches C libraries through
          # goffi/purego's cgo_import_dynamic bridge. The default (untagged)
          # goffi mode is a dynamic FFI build: PT_INTERP plus DT_NEEDED on
          # libdl/libc/libpthread. That is the right mode for Nix - unlike the
          # release's goffi_universal flavour it needs no host loader at
          # well-known paths (absent on NixOS), and unlike goffi_static it
          # keeps the FFI-backed GUI/audio backends alive.
          #
          # The Go toolchain hardcodes the upstream dynamic linker, so the
          # interpreter is repointed at Nix's glibc loader with the Go
          # linker's own -I flag. No RPATH is needed: the loader resolves
          # libc.so.6/libdl.so.2/libpthread.so.0 from its own directory.
          # The interpreter must NOT be set with patchelf, and patchelf must
          # not touch the binary at all: rewriting this internally linked Go
          # binary relocates .dynamic into the non-writable text segment, and
          # ld.so then faults writing DT_DEBUG. Hence dontPatchELF below.
          env.CGO_ENABLED = "0";
          dontPatchELF = true;

          # Must track go.mod/go.sum: after a dependency change nix build
          # fails with "hash mismatch in fixed-output derivation ... got:
          # sha256-...", and that got: value is the new vendorHash.
          vendorHash = "sha256-2wRn7R0X2YGOQ1bnfN53K3ql7mu3+Vg/90kbA9pmEPo=";

          subPackages = [ "cmd/f4" ];
          tags = lib.optionals ttyOnly [ "tty_only" "vtui_noebiten" "vtui_nogogpu" ];

          ldflags = [
            "-s"
            "-w"
            "-X github.com/unxed/f4/internal/app.buildVersion=${version}"
          ] ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [
            "-I"
            "${pkgs.stdenv.cc.bintools.dynamicLinker}"
          ];

          # The suite drives real ptys (tools/ttytest) and GUI backends;
          # keep it out of the build sandbox.
          doCheck = false;

          postInstall = let docDir = if ttyOnly then "f4-tty" else "f4"; in ''
            install -Dm644 f4.example.ini README.md -t $out/share/doc/${docDir}
            install -Dm644 plugins/visren/LICENSE.upstream $out/share/doc/${docDir}/licenses/VisRen-BSD-3-Clause.txt
            install -Dm644 plugins/ios/LICENSE.go-ios $out/share/doc/${docDir}/licenses/go-ios-MIT.txt
          '' + lib.optionalString ttyOnly ''
            mkdir -p $out/libexec/f4-tty
            mv $out/bin/f4 $out/libexec/f4-tty/f4-tty
            install -Dm644 internal/dialog/help/*.hlf -t $out/libexec/f4-tty/help
            cat > $out/bin/f4-tty <<EOF
            #!${pkgs.runtimeShell}
            exec "$out/libexec/f4-tty/f4-tty" "\$@"
            EOF
            chmod +x $out/bin/f4-tty
          '' + lib.optionalString (!ttyOnly) ''
            # Only the English help text is embedded; the other .hlf files are
            # found at runtime in dirname(argv[0])/help, the same layout as the
            # release archives.
            install -Dm644 internal/dialog/help/*.hlf -t $out/bin/help
            cat > $out/bin/f4-gui <<'EOF'
            #!${pkgs.runtimeShell}
            exec "$(dirname "$0")/f4" --gui "$@"
            EOF
            chmod +x $out/bin/f4-gui
          '' + lib.optionalString (!ttyOnly && pkgs.stdenv.hostPlatform.isLinux) ''
            install -Dm644 packaging/linux/org.unxed.f4.desktop -t $out/share/applications
            for size in 16 24 28 30 32 36 42 48 56 64 128 256 512 1024; do
              install -Dm644 internal/gui/assets/icon/generated/f4-''${size}.png \
                $out/share/icons/hicolor/''${size}x''${size}/apps/io.github.unxed.f4.png
            done
            install -Dm644 internal/gui/assets/icon/f4.svg \
              $out/share/icons/hicolor/scalable/apps/io.github.unxed.f4.svg
          '';

          meta = {
            description = if ttyOnly then "Terminal-only f4 file manager" else "f4 file manager with terminal and graphical modes";
            homepage = "https://github.com/unxed/f4";
            license = lib.licenses.bsd3;
            mainProgram = if ttyOnly then "f4-tty" else "f4";
            platforms = lib.platforms.linux ++ lib.platforms.darwin;
          };
        };
      in rec {
          default = f4-gui;
          f4 = f4-gui;
          f4-gui = mkF4 false;
          f4-tty = mkF4 true;
        });

      overlays.default = final: _: {
        f4 = self.packages.${final.stdenv.hostPlatform.system}.default;
        f4-gui = self.packages.${final.stdenv.hostPlatform.system}.f4-gui;
        f4-tty = self.packages.${final.stdenv.hostPlatform.system}.f4-tty;
      };

      homeManagerModules = {
        default = self.homeManagerModules.f4;
        f4 = import ./packaging/nix/home-manager-module.nix;
      };

      checks = forAllSystems ({ pkgs, ... }: {
        f4-gui = self.packages.${pkgs.stdenv.hostPlatform.system}.f4-gui;
        f4-tty = self.packages.${pkgs.stdenv.hostPlatform.system}.f4-tty;
        home-manager-module = import ./packaging/nix/home-manager-module-check.nix {
          inherit pkgs home-manager;
        };
      });

      devShells = forAllSystems ({ pkgs, ... }: {
        default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.golangci-lint ];
        };
      });

      apps = forAllSystems ({ system, ... }: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/f4";
          meta.description = "f4 file manager";
        };
        gui = {
          type = "app";
          program = "${self.packages.${system}.f4-gui}/bin/f4-gui";
          meta.description = "f4 graphical window";
        };
        tty = {
          type = "app";
          program = "${self.packages.${system}.f4-tty}/bin/f4-tty";
          meta.description = "f4 terminal-only build";
        };
      });
    };
}
