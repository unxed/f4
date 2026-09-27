#!/bin/bash

# Builds a wasm32-wasip1 test fixture out of github.com/lazyhamster/Observer's
# isoimg module (ISO9660/Joliet/RockRidge image reader, f4#1563 part 2), for
# plugins/observer's end-to-end test that drives it through the real wazero
# host in runtime.go -- not the from-scratch stub testdata/stub/observer_stub.c
# exercises, but an actual, unmodified upstream Observer module.
#
# f4 does not vendor isoimg's own LGPL/GPL-adjacent sources (see f4#1563's
# recon comment on Observer/ObserverModules licensing) or embed them in any
# f4 binary: this script fetches them from upstream at a pinned commit into a
# scratch directory, compiles them there together with f4's own BSD-licensed
# compat shim (plugins/observer/testdata/isoimg/compat/, see its windows.h
# and doc comments for what is and is not ported), and writes only the
# resulting .wasm -- itself not checked into the repository, like
# testdata/observer_stub.wasm (see .gitignore) -- to testdata/isoimg_test.wasm.
# ISZ support (isz/*.cpp: zlib+bzip2, plus AES under GPL-2) is deliberately
# left out; compat/isz_stub.cpp makes isoimg fall back to its ordinary raw
# ISO9660 path instead, which is all this end-to-end test needs.
#
# This is a build step, not a build of f4 itself: nothing here is compiled on
# a contributor's machine, only in CI (see AGENTS.md and
# scripts/build_observer_test_wasm.sh, whose pattern this script follows).
#
# Default path:
#   WASI_SDK_PATH=/opt/wasi-sdk
#
# Override it, and optionally the pinned commit, if necessary:
#   WASI_SDK_PATH=/path/to/wasi-sdk ./scripts/build_isoimg_test_wasm.sh

set -e

WASI_SDK_PATH="${WASI_SDK_PATH:-/opt/wasi-sdk}"
CLANGXX="$WASI_SDK_PATH/bin/clang++"

if [ ! -x "$CLANGXX" ]; then
    echo
    echo "Error: WASI SDK clang++ was not found at:"
    echo "  $CLANGXX"
    echo
    echo "Please set WASI_SDK_PATH to your WASI SDK installation."
    echo "For example:"
    echo "  WASI_SDK_PATH=/path/to/wasi-sdk ./scripts/build_isoimg_test_wasm.sh"
    exit 1
fi

# The upstream commit this script builds against. Observer's isoimg has not
# needed a source change since (see f4#1563's recon comment: last upstream
# commit May 2023); pinned so this script's behavior does not depend on
# whatever happens to be at the tip of master-gh on the day it runs.
OBSERVER_REPO="https://github.com/lazyhamster/Observer.git"
OBSERVER_COMMIT="ddbe6b6d13a51c09d14c565e110b3e6b8b06a69a"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OBSERVER_DIR="$REPO_ROOT/plugins/observer"
COMPAT_DIR="$OBSERVER_DIR/testdata/isoimg/compat"
OUT="$OBSERVER_DIR/testdata/isoimg_test.wasm"

SYSROOT="$WASI_SDK_PATH/share/wasi-sysroot"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "Fetching lazyhamster/Observer @ $OBSERVER_COMMIT into $WORKDIR"
git -C "$WORKDIR" init -q
git -C "$WORKDIR" remote add origin "$OBSERVER_REPO"
git -C "$WORKDIR" fetch -q --depth 1 origin "$OBSERVER_COMMIT"
git -C "$WORKDIR" checkout -q FETCH_HEAD

SRC="$WORKDIR/src/modules/isoimg"
COMMON="$WORKDIR/src/common"
DEPENDS="$WORKDIR/src/depends"
MODULECRT="$DEPENDS/modulecrt"

echo "Using WASI SDK: $WASI_SDK_PATH"
echo "Building: isoimg ($SRC) -> $OUT"

# --target/--sysroot given explicitly for the same reason
# build_observer_test_wasm.sh gives them: wazero's wasi_snapshot_preview1
# package is the classic WASI "preview 1" ABI, and some wasi-sdk releases
# default clang to a newer preview.
#
# -I order matters: COMPAT_DIR first so every quote-include of "StdAfx.h"
# (see compat/StdAfx.h) and <windows.h> (see compat/windows.h) resolves to
# f4's shim rather than failing to find upstream's differently-cased or
# nonexistent file, ahead of SRC/COMMON/DEPENDS for isoimg's own headers,
# ModuleDef.h, and isoimg.cpp's own "modulecrt/OptionsParser.h" (DEPENDS is
# modulecrt's parent, matching that quote-include's own subpath).
#
# -fno-exceptions/-fno-rtti: isoimg has no throw/catch of its own (confirmed
# during f4#1563's recon -- only false positives on "entry"/"entries"), so it
# builds the same way colorer4go's C++ does today, ahead of real exception
# support landing in wazero (see f4#1563's plan item 4).
"$CLANGXX" \
    --target=wasm32-wasip1 \
    --sysroot="$SYSROOT" \
    -O2 \
    -std=c++17 \
    -fno-exceptions \
    -fno-rtti \
    -mexec-model=reactor \
    -I "$COMPAT_DIR" \
    -I "$SRC" \
    -I "$COMMON" \
    -I "$DEPENDS" \
    -Wl,--export=LoadSubModule \
    -Wl,--export=UnloadSubModule \
    -Wl,--export=f4observer_open_storage \
    -Wl,--export=f4observer_close_storage \
    -Wl,--export=malloc \
    -Wl,--export=free \
    -o "$OUT" \
    "$SRC/isoimg.cpp" \
    "$SRC/iso_ext.cpp" \
    "$SRC/iso_tc.cpp" \
    "$MODULECRT/OptionsParser.cpp" \
    "$COMPAT_DIR/isz_stub.cpp" \
    "$COMPAT_DIR/trampolines.cpp"

echo "Success!"
echo "WASM binary: $OUT"
ls -l "$OUT"
