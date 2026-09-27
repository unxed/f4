module github.com/unxed/f4

go 1.26.6

require (
	github.com/abadojack/whatlanggo v1.0.1
	github.com/alecthomas/chroma/v2 v2.15.0
	github.com/charlievieth/strcase v0.0.6
	github.com/coregx/coregex v0.12.19
	github.com/ebitengine/oto/v3 v3.5.0-alpha.9.0.20260810052149-c311bfa6e535
	github.com/ebitengine/purego v0.11.0-alpha.8
	github.com/go-webgpu/goffi v0.6.3
	github.com/hajimehoshi/go-mp3 v0.3.4
	github.com/hanwen/go-fuse/v2 v2.11.0
	github.com/jezek/xgb v1.3.1
	github.com/jfreymuth/oggvorbis v1.0.5
	github.com/jlaffaye/ftp v0.2.0
	github.com/kbolino/pageant v0.0.0-20180919004629-179b60797d9f
	github.com/mattn/go-runewidth v0.0.15
	github.com/mewkiz/flac v1.0.14
	github.com/mholt/archives v0.1.5
	github.com/pkg/sftp v1.13.6
	github.com/tetratelabs/wazero v1.12.0
	github.com/unxed/archives v0.1.3
	github.com/unxed/colorer4go v0.1.24
	github.com/unxed/ffibridge v0.1.5
	github.com/unxed/go2xp v0.0.0-20260918000857-00a22a520369
	github.com/unxed/id3-go v0.1.2
	github.com/unxed/libwinescape v0.2.1
	github.com/unxed/localecp v0.1.6
	github.com/unxed/sevenzip v0.1.7
	github.com/unxed/tar v0.1.137
	github.com/unxed/vtinput v0.1.8
	github.com/unxed/vtui v0.1.370
	github.com/unxed/zip v0.1.143
	github.com/unxed/zipper v0.1.176
	github.com/vmihailenco/msgpack/v5 v5.4.1
	github.com/woozymasta/png v1.2.0
	github.com/yuin/gopher-lua v1.1.1
	github.com/zzl/go-win32api/v2 v2.1.0
	golang.org/x/arch v0.30.0
	golang.org/x/crypto v0.56.0
	golang.org/x/image v0.45.0
	golang.org/x/net v0.58.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	golang.org/x/text v0.41.0
	gopkg.in/yaml.v3 v3.0.1
)

// f4#1178 part 1: aws-sdk-go-v2 (+ its config/credentials/feature/service
// submodules), aws/smithy-go, zalando/go-keyring, google.golang.org/api and
// golang.org/x/oauth2 were direct requires only for plugins/cloudfox, which
// is now its own module (plugins/cloudfox/go.mod) and no longer part of
// this build. The indirect require block below still lists some of their
// own transitive dependencies (cloud.google.com/go/auth, the
// go.opentelemetry.io/* set, google.golang.org/grpc, ...); nothing in this
// module imports them any more, so they compile into nothing, but a
// `go mod tidy` pass (in CI, not locally -- see build-cloudfox-plugin in
// .github/workflows/build.yml) is expected follow-up to prune the now-stale
// entries rather than something this PR hand-edited blind.

// f4#1178 iOS plugin, part 1 of 4: github.com/danielpaulus/go-ios,
// github.com/Masterminds/semver (a go-ios dependency) and github.com/google/uuid
// (used by plugins/ios/internal/corefileservice) were direct requires only
// for plugins/ios, which is now its own module (plugins/ios/go.mod) and no
// longer part of this build, in either the full or the lite build. go-ios's
// own, fully separate dependency chain -- gvisor.dev/gvisor, quic-go,
// vishvananda/netlink+netns, songgao/water, miekg/dns, grandcat/zeroconf,
// howett.net/plist, go.mozilla.org/pkcs7, software.sslmate.com/src/go-pkcs12,
// golang.zx2c4.com/wintun -- is still listed below as (now stale) indirect
// version pins for the same reason the cloudfox-era entries above are: a
// `go mod tidy` pass (in CI, not locally -- see build-ios-plugin in
// .github/workflows/build.yml) is expected follow-up to prune them, and
// cmd/f4/ios_deps_test.go is the mechanical proof that nothing in this
// module imports them any more.

require (
	github.com/ebitengine/gomobile v0.0.0-20260211053922-3d992dae95d1 // indirect
	github.com/ebitengine/hideconsole v1.0.0 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/hajimehoshi/ebiten/v2 v2.10.0-alpha.13.0.20260811162617-464c2ddfc34c // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/jfreymuth/pulse v0.1.2 // indirect
	github.com/jfreymuth/vorbis v1.0.2 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/soniakeys/quant v1.0.0 // indirect
	github.com/unxed/goclip v0.1.2 // indirect
	github.com/unxed/kiwi-go v0.1.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

require (
	github.com/STARRY-S/zip v0.2.3 // indirect
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/sevenzip v1.6.4 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/coregx/ahocorasick v0.2.1 // indirect
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/dsnet/compress v0.0.2-0.20230904184137-39efe44ab707 // indirect
	github.com/emmansun/base64 v0.9.0 // indirect
	github.com/fogleman/gg v1.3.0 // indirect
	github.com/go-webgpu/webgpu v0.5.5 // indirect
	github.com/gogpu/gg v0.52.5 // indirect
	github.com/gogpu/gogpu v0.53.0 // indirect
	github.com/gogpu/gpucontext v0.28.0 // indirect
	github.com/gogpu/gputypes v0.5.2 // indirect
	github.com/gogpu/naga v0.18.0 // indirect
	github.com/gogpu/wgpu v0.31.6 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/klauspost/compress v1.19.2
	github.com/klauspost/pgzip v1.2.6
	github.com/kr/fs v0.1.0 // indirect
	github.com/mikelolasagasti/xz v1.0.1 // indirect
	github.com/minio/minlz v1.1.1 // indirect
	github.com/ncruces/go-sqlite3 v0.35.2
	github.com/ncruces/go-sqlite3-wasm/v3 v3.2.35303 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/neurlang/wayland v0.4.4 // indirect
	github.com/neurlang/winc v0.1.2 // indirect
	github.com/nwaples/rardecode/v2 v2.4.1
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/rivo/uniseg v0.2.0
	github.com/sorairolake/lzip-go v0.3.8 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/stangelandcl/ppmd v0.1.1 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	github.com/unxed/keytrans v0.1.33
	github.com/unxed/par2 v0.1.3 // indirect
	github.com/unxed/winkeys v0.1.1
	github.com/unxed/xkb-go v0.1.8 // indirect
	github.com/unxed/xz v0.1.47 // indirect
	github.com/unxed/zipcharset v0.1.5 // indirect
	github.com/unxed/zlib4go v0.1.16 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/yalue/native_endian v1.0.2 // indirect
	go4.org v0.0.0-20260112195520-a5071408f32f // indirect
	golang.design/x/clipboard v0.7.0 // indirect
	golang.org/x/exp/shiny v0.0.0-20260727155853-b88d891fe743 // indirect
	golang.org/x/mobile v0.0.0-20260611195102-4dd8f1dbf5d2 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

replace github.com/ebitengine/purego => github.com/unxed/pureffi v0.1.21

replace github.com/ebitengine/hideconsole => ./internal/hideconsole

replace github.com/go-webgpu/goffi => github.com/unxed/goffi v0.1.11
