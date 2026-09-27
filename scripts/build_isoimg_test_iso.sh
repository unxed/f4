#!/bin/bash

# Builds the tiny, plain ISO9660 image plugins/observer's isoimg end-to-end
# test (f4#1563, part 2) probes isoimg_test.wasm against, using genisoimage
# (Debian/Ubuntu package "genisoimage") the same way scripts/build_isoimg_
# test_wasm.sh builds the module: a CI-only step, never checked in (see
# .gitignore), never run on a contributor's machine.
#
# The image is deliberately plain Level-1/2 ISO9660 -- no -R (Rock Ridge)
# and no -J (Joliet) -- so isoimg.cpp's OpenStorage takes its ordinary
# 8.3-name, non-Unicode path (image->VolumeDescriptors->Unicode == false,
# SystemUseAreas == false), the simplest one it has and the one this
# end-to-end test's compat layer (plugins/observer/testdata/isoimg/compat/)
# was written against. genisoimage's own ISO9660 writer, not any bespoke
# byte-laid-out fixture, is what this test trusts to produce a spec-correct
# image -- isoimg reading a genisoimage-authored disc is exactly the case it
# has handled in the real Observer/Far Manager for years.

set -e

GENISOIMAGE="${GENISOIMAGE:-genisoimage}"
if ! command -v "$GENISOIMAGE" >/dev/null 2>&1; then
    echo
    echo "Error: $GENISOIMAGE was not found on PATH."
    echo "Install it first, e.g.: sudo apt-get install -y genisoimage"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT="$REPO_ROOT/plugins/observer/testdata/isoimg_test.iso"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

mkdir -p "$WORKDIR/root"
printf 'hello from the f4#1563 isoimg end-to-end test\n' > "$WORKDIR/root/HELLO.TXT"

"$GENISOIMAGE" -quiet -o "$OUT" -V F4TESTVOL "$WORKDIR/root"

echo "Success!"
echo "ISO image: $OUT"
ls -l "$OUT"
