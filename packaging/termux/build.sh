# Recipe for the official Termux repository (termux/termux-packages,
# packages/f4/build.sh): `pkg install f4` instead of downloading the .deb of a
# release by hand. It is what would be submitted there; it is not built or
# tested in Termux's own build system yet (unxed/f4#12), so treat every line
# as unverified until a maintainer of termux-packages has run
# `./build-package.sh f4`. The .deb published with each f4 release is built by
# the build-termux job of .github/workflows/build.yml and stays the supported
# way until then.
TERMUX_PKG_HOMEPAGE=https://github.com/unxed/f4
TERMUX_PKG_DESCRIPTION="Efficient and cozy two-panel file manager"
TERMUX_PKG_LICENSE="BSD 3-Clause"
TERMUX_PKG_LICENSE_FILE="LICENSE"
TERMUX_PKG_MAINTAINER="@unxed"
_UPSTREAM_TAG="v0.3.0-beta"
TERMUX_PKG_VERSION="0.3.0~beta"
TERMUX_PKG_SRCURL="https://github.com/unxed/f4/archive/refs/tags/${_UPSTREAM_TAG}.tar.gz"
TERMUX_PKG_SHA256=ca7b2c711cc3d4dc14e63a0b08b0098e58e0c40b75441e44f828a8ec7ae1b4be
TERMUX_PKG_AUTO_UPDATE=false
TERMUX_PKG_BUILD_IN_SRC=true

termux_step_make() {
	termux_setup_golang

	# cgo against Bionic: the binary must be dynamic with $PREFIX/lib as its
	# RUNPATH, which is what separates it from the useless pure-Go build
	# (the same contract the release job checks with readelf).
	export CGO_ENABLED=1
	go build -buildmode=pie -trimpath \
		-ldflags "-s -w -r ${TERMUX_PREFIX}/lib -X github.com/unxed/f4/internal/app.buildVersion=${_UPSTREAM_TAG}" \
		-o f4 ./cmd/f4
}

termux_step_make_install() {
	local share="${TERMUX_PREFIX}/share/f4"
	install -Dm700 f4 "${share}/f4"
	ln -sfr "${share}/f4" "${TERMUX_PREFIX}/bin/f4"
	cp -r internal/i18n/lang internal/dialog/help "${share}/"
	install -Dm600 f4.example.ini "${share}/f4.example.ini"
	install -Dm600 plugins/visren/LICENSE.upstream \
		"${share}/licenses/VisRen-BSD-3-Clause.txt"
}
