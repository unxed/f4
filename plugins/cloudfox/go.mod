module github.com/unxed/f4/plugins/cloudfox

go 1.26.6

// This module builds the downloadable plugin used by lite f4 builds.
// Full f4 builds import this module in-process through a local replace.
// The plugin uses the checkout's shared SDK and VFS through the replace below.
require (
	github.com/aws/aws-sdk-go-v2 v1.43.7
	github.com/aws/aws-sdk-go-v2/config v1.32.38
	github.com/aws/aws-sdk-go-v2/credentials v1.19.37
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.22.42
	github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager v0.3.15
	github.com/aws/aws-sdk-go-v2/service/s3 v1.107.3
	github.com/aws/smithy-go v1.27.8
	github.com/google/uuid v1.6.0 // indirect
	github.com/unxed/f4 v0.0.0
	github.com/unxed/vtinput v0.1.11
	github.com/unxed/vtui v0.1.417-0.20261021153038-34e1d23b5d91
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/crypto v0.57.0
	golang.org/x/net v0.60.0
	golang.org/x/oauth2 v0.36.0
	google.golang.org/api v0.264.0
)

require golang.org/x/sys v0.48.0

require (
	cloud.google.com/go/auth v0.18.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/abadojack/whatlanggo v1.0.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.18 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.39 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.38 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.39 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.7 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charlievieth/strcase v0.0.6 // indirect
	github.com/coregx/ahocorasick v0.2.1 // indirect
	github.com/coregx/coregex v0.12.19 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/ebitengine/gomobile v0.0.0-20260211053922-3d992dae95d1 // indirect
	github.com/ebitengine/hideconsole v1.0.0 // indirect
	github.com/ebitengine/purego v0.11.0-alpha.8 // indirect
	github.com/emmansun/base64 v0.9.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fogleman/gg v1.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-webgpu/goffi v0.6.3 // indirect
	github.com/go-webgpu/webgpu v0.5.5 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gogpu/gg v0.52.5 // indirect
	github.com/gogpu/gogpu v0.53.0 // indirect
	github.com/gogpu/gpucontext v0.28.0 // indirect
	github.com/gogpu/gputypes v0.5.2 // indirect
	github.com/gogpu/naga v0.18.0 // indirect
	github.com/gogpu/wgpu v0.31.6 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.11 // indirect
	github.com/googleapis/gax-go/v2 v2.17.0 // indirect
	github.com/hajimehoshi/ebiten/v2 v2.10.0-alpha.13.0.20260811162617-464c2ddfc34c // indirect
	github.com/jezek/xgb v1.3.1 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/neurlang/wayland v0.4.5-0.20261007184820-37f4fac9bad0 // indirect
	github.com/neurlang/winc v0.1.2 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/soniakeys/quant v1.0.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/unxed/goclip v0.1.3-0.20261020202204-ff732bd92518 // indirect
	github.com/unxed/keytrans v0.1.35 // indirect
	github.com/unxed/kiwi-go v0.1.0 // indirect
	github.com/unxed/libwinescape v0.2.1 // indirect
	github.com/unxed/localecp v0.1.7 // indirect
	github.com/unxed/winkeys v0.1.1 // indirect
	github.com/unxed/xkb-go v0.1.9 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/yalue/native_endian v1.0.2 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	github.com/zzl/go-win32api/v2 v2.1.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.61.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.design/x/clipboard v0.7.0 // indirect
	golang.org/x/exp/shiny v0.0.0-20260727155853-b88d891fe743 // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/mobile v0.0.0-20260611195102-4dd8f1dbf5d2 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/unxed/f4 => ../..

// Same forks the root module uses (see ../../go.mod) -- vtui transitively
// needs ffi.Available, which only exists in unxed/pureffi, not upstream
// ebitengine/purego. Without these, `go mod tidy` here resolves the
// vanilla upstream modules instead and the build fails with
// "undefined: ffi.Available".
replace github.com/ebitengine/purego => github.com/unxed/pureffi v0.1.21

replace github.com/ebitengine/hideconsole => ../../internal/hideconsole

replace github.com/go-webgpu/goffi => github.com/unxed/goffi v0.1.11

// gogpu with the drag callbacks vtui calls (#2049, see ../../go.mod).
replace github.com/gogpu/gogpu => github.com/sogonov/gogpu v0.53.1-drag.1

replace github.com/neurlang/wayland => github.com/unxed/wayland v0.4.5-0.20260924170549-04f3e691fadc
