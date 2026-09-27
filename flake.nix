{
  description = "f4 - efficient and cozy file manager in Go (Far Manager / far2l-style UX)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
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
      packages = forAllSystems ({ pkgs, system }: rec {
        default = f4;
        f4 = pkgs.buildGoModule {
          pname = "f4";
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
          vendorHash = "sha256-htSSNdM+WrPt0O0sEh1UWVzEQeYjaO6UtB6TSUMUL5s=";

          subPackages = [ "cmd/f4" ];

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

          postInstall = ''
            install -Dm644 packaging/linux/f4.desktop -t $out/share/applications
            install -Dm644 f4.example.ini README.md -t $out/share/doc/f4
            install -Dm644 plugins/visren/LICENSE.upstream $out/share/doc/f4/licenses/VisRen-BSD-3-Clause.txt
            install -Dm644 plugins/ios/LICENSE.go-ios $out/share/doc/f4/licenses/go-ios-MIT.txt
          '' + lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
            for size in 16 24 28 30 32 36 42 48 56 64 128 256 512 1024; do
              install -Dm644 internal/gui/assets/icon/generated/f4-''${size}.png \
                $out/share/icons/hicolor/''${size}x''${size}/apps/io.github.unxed.f4.png
            done
            install -Dm644 internal/gui/assets/icon/f4.svg \
              $out/share/icons/hicolor/scalable/apps/io.github.unxed.f4.svg
            # Only the English help text is embedded; the other .hlf files are
            # found at runtime in dirname(argv[0])/help, the same layout as the
            # release archives.
            install -Dm644 internal/dialog/help/*.hlf -t $out/bin/help
          '';

          meta = {
            description = "TUI file manager reproducing the UX of far2l and Far Manager";
            homepage = "https://github.com/unxed/f4";
            license = lib.licenses.bsd3;
            mainProgram = "f4";
            platforms = lib.platforms.linux ++ lib.platforms.darwin;
          };
        };
      });

      overlays.default = final: _: {
        f4 = self.packages.${final.stdenv.hostPlatform.system}.default;
      };

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
      });
    };
}
