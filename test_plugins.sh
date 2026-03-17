#!/bin/bash
set -e

echo "1. Downloading Go dependencies..."
go mod tidy

echo "2. Building Go WASM plugin..."
cd plugins/hello_go
GOOS=wasip1 GOARCH=wasm go build -o hello_go.wasm .
cd ../..

echo "3. Building C WASM plugin (requires clang)..."
if command -v clang >/dev/null 2>&1; then
    cd plugins/hello_c
    clang --target=wasm32-wasi -nostdlib -Wl,--no-entry -Wl,--export=SetStartupInfoW -o hello_c.wasm main.c
    cd ../..
    echo "   C WASM built successfully."
else
    echo "   WARNING: clang not found. Skipping C WASM build."
fi

echo "4. Running f4 in test mode (Output will go to debug.log)..."
rm -f debug.log
VTUI_DEBUG=1 go run . -test-plugins

echo ""
echo "=== debug.log Output ==="
cat debug.log | grep PLUGIN
