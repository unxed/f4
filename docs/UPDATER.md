# Updater

`internal/update` checks GitHub for a newer build and installs it over the
running one, from the update dialog or from `f4 --update [stable|nightly]`.
This document is about the part that is easy to break for everybody at once:
which release asset an installed f4 takes.

## Release asset names are an interface

Every f4 installed anywhere reads each new release with the asset rule its
own updater was built with. A rule that has shipped cannot be changed: a name
that one of them misreads reaches all of its users at once, and the fix only
reaches them if they can still update. #1656 was two such names. Plugin
archives (`android-plugin-linux-amd64.tar.gz`) and the lite Windows archive
(`f4-lite-windows-amd64.zip`) sorted before f4's own, and were installed in
its place. f4 was left on the old build, and the updater recorded the update
as done.

So a release's file names are an interface with every build ever released,
not only with the current code.

Release archives now put their contents below a single `f4/` directory, so a
manual extraction creates one tidy directory instead of scattering files into
the current directory. The updater strips that wrapper while installing over
the running binary, and still accepts the flat archives published before this
layout change.

## How a build picks its archive

The API lists a release's assets by name. The build asks for a list of
suffixes, most preferred first, and takes the first asset that ends with one:

| Build | Asks for |
| --- | --- |
| Linux, BSDs, macOS, illumos, Solaris | `-<os>-<arch>.tar.gz` |
| Linux musl build | `-linux-musl-<arch>.tar.gz`, then `-linux-<arch>.tar.gz` |
| Android (Termux) | `-termux-<arch>.tar.gz` |
| Windows | `-windows-<arch>.7z`, then `-windows-<arch>.zip` |
| Windows 7/8/8.1 build (`-tags win7`) | `-windows7-<arch>.7z`, then `-windows7-<arch>.zip` |
| lite edition (`-tags lite`), any OS | `-lite-<os>-<arch>.tar.gz` |

The current code also skips any name that does not begin with `f4-`, and a
regular build skips `-lite-` names (`pickAsset`, `takesAsset`).

## The rules still installed

`update.generations` lists every rule that builds in the field still use:

| Generation | Rule |
| --- | --- |
| v0.1.2-alpha, v0.1.3-alpha | Windows: only `.zip`; no Termux, musl, lite; no filters |
| v0.2.0-beta to v0.3.0-beta, nightlies before 2026-09-27 | `.7z` before `.zip`; no lite check, no `f4-` prefix |
| nightlies from 2026-09-27 to the #1656 fix | lite check; lite Windows asks for `.zip` |
| current code | `f4-` prefix and lite check |

Hence today's layout:

- Plugin archives are `.tgz`, so no rule matches them.
- Every lite archive is `.tar.gz`, Windows included. No Windows rule of any
  generation asks for `.tar.gz`, and `f4-lite-windows-amd64.zip` sorted before
  `f4-windows-amd64.zip`.
- Windows is published as `.7z` as well as `.zip`: v0.2.0-beta and later take
  the `.7z`, v0.1.x needs the `.zip`.
- On Linux, `f4-linux-*` sorts before `f4-lite-linux-*`. The old rules rely on
  that order, and the check after publishing verifies it.

Knowingly left behind (each needs one reinstall by hand):

- lite Windows builds from the nightlies of 2026-09-27 to the #1656 fix. They
  ask for `-lite-windows-amd64.zip`, the name that turned regular builds into
  lite ones.
- Windows 7/8/8.1 builds released before `-tags win7`. They ask exactly as a
  regular windows/amd64 build does, so no name can give them their own
  archive.

## Is there anything to install

On Linux, the updater checks whether pacman owns the running executable before
contacting the release API. A pacman-managed build is left to pacman or the AUR
helper that installed it; the built-in updater refuses to replace it, because
doing so would leave the package manager's recorded files out of sync.

The running binary is asked first (`Check`). On the nightly channel it
compares its own commit and that commit's time, which `f4 -v` prints, with
the `Commit:` and `Built on:` the nightly's notes give (`nightlyNeedsUpdate`):
the same commit is up to date, an older one is offered the nightly, a newer
one is not sent back. On stable a release build compares its tag, and a
development build its commit time with the release's publication time.

`[Update] LastVersion` in settings.ini is the asset time (nightly) or tag
(stable) that the updater stored after its own last install. It decides only
when the binary cannot answer: a release build on the nightly channel, a
build without VCS data, nightly notes without the commit. It knows nothing of
an f4 unpacked by hand, which `f4 --update` used to download again (#2172).

`f4 --update` stops on Ctrl+C (Ctrl+Break on Windows) while it checks or
downloads, with nothing changed. From the backup of the running binary on,
it lets the install finish: stopped halfway, f4 would be left half replaced.

## Safeguards

Before publishing, the nightly and release jobs run
`go run ./tools/releasecheck dist`. The tool:

1. replays every generation over the file names (`update.AuditRelease`), for
   every platform in `update.installedFlavors` and any the release adds. Each
   must take an archive the current code accepts for its platform and edition;
   anything else, or nothing, fails;
2. unpacks every archive they would take with the updater's own code, over a
   stand-in for the executable (`update.CheckReleaseArchive`), and fails
   unless it is replaced;
3. reads the new executable's Go build information and fails unless its
   GOOS, GOARCH and edition tag (`lite`, `win7`, `go2xp` for the legacy
   build, none for the regular one) match the archive's name.

On any failure nothing is published, and the previous release stays.

After publishing, the jobs read the release back from the API and replay the
generations in the order the API returned (`releasecheck -listed`). If any
generation would misread the release, it is turned back into a draft. The
nightly channel then answers 404, and `latest` falls back to the previous
release. The job fails.

On the user's side:

- `Install` fails when the archive did not replace the running executable,
  so a wrong archive is never recorded as installed.
- `CheckInstalled` runs the new build with `--version`. If it does not start,
  the dialog and `f4 --update` put the previous build back. Anything that
  handles `--version` must keep it exiting cleanly, before any UI starts.

## Changing any of this

- Renaming or adding a release asset: run the release check over the new
  layout. A generation that misreads it cannot be fixed afterwards; rename
  the asset instead.
- Changing `pickAsset`, `assetSuffixes`, `editionAssetSuffixes` or
  `releaseOS`: add a generation for the builds released before the change.
- Dropping a platform: remove it from `installedFlavors` on purpose. Its
  installed builds stop receiving updates.
- A new edition: give it a build tag, add the tag to `editionTags`, and name
  its archives so that no generation above takes them for another edition.
