#!/bin/bash

# Validates plugins/observer's isoimg end-to-end test fixture (f4#1563, part
# 2) against the wasm spec, and reports what wasm-opt -Oz would shrink it to,
# using Binaryen's own prebuilt release (never built from source here -- see
# BINARYEN_PATH below).
#
# Binaryen's distribution has no standalone "wasm-validate" binary: wasm-opt
# itself parses and validates a module before doing anything else with it
# (validation is not something -Oz turns on -- an invalid module makes
# wasm-opt fail the same way with no optimization flags at all), so running
# it once with no transformation, then again with -Oz, both validates the
# module and measures -Oz's effect.
#
# This is a report/gate step, not a rewrite: it does not replace
# isoimg_test.wasm with the -Oz output, only prints the size difference, so
# a future part can decide whether shipping real modules -Oz'd is worth the
# extra build step.
#
# Default path:
#   BINARYEN_PATH=/opt/binaryen
#
# Override it if necessary:
#   BINARYEN_PATH=/path/to/binaryen ./scripts/check_isoimg_wasm.sh path/to/module.wasm

set -e

BINARYEN_PATH="${BINARYEN_PATH:-/opt/binaryen}"
WASM_OPT="$BINARYEN_PATH/bin/wasm-opt"

if [ ! -x "$WASM_OPT" ]; then
    echo
    echo "Error: Binaryen's wasm-opt was not found under:"
    echo "  $BINARYEN_PATH"
    echo
    echo "Please set BINARYEN_PATH to your Binaryen installation."
    exit 1
fi

WASM="${1:?usage: check_isoimg_wasm.sh path/to/module.wasm}"

VALIDATED_OUT="$(mktemp -u).wasm"
OPT_OUT="$(mktemp -u).wasm"
trap 'rm -f "$VALIDATED_OUT" "$OPT_OUT"' EXIT

echo "Validating $WASM with wasm-opt ($("$WASM_OPT" --version))"
"$WASM_OPT" "$WASM" -o "$VALIDATED_OUT"
echo "wasm-opt: OK, $WASM is a spec-valid wasm module."

ORIG_SIZE=$(stat -c%s "$WASM" 2>/dev/null || stat -f%z "$WASM")

"$WASM_OPT" -Oz "$WASM" -o "$OPT_OUT"
OPT_SIZE=$(stat -c%s "$OPT_OUT" 2>/dev/null || stat -f%z "$OPT_OUT")

echo "wasm-opt -Oz: $ORIG_SIZE -> $OPT_SIZE bytes ($(( (ORIG_SIZE - OPT_SIZE) * 100 / ORIG_SIZE ))% smaller)"
