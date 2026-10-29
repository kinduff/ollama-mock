#!/bin/sh
# Builds the mock to WebAssembly next to the page, with the wasm_exec.js of the same Go.
set -e
cd "$(dirname "$0")"
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o public/ollama-mock.wasm ../cmd/ollama-mock-wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" public/
