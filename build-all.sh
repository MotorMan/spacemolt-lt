#!/usr/bin/env bash
# Cross-compile build script for Linux/macOS
# Usage: ./build-all.sh

set -euo pipefail

MODULE="spacemolt-lt"
OUTDIR="dist"
VERSION="1.0.0"

echo "=== SpaceMolt Bot Runner - Cross-Compile Build ==="

# Clean output dir
rm -rf "$OUTDIR"
mkdir -p "$OUTDIR"

targets=(
    "windows amd64 .exe"
    "linux   amd64 ''"
    "linux   arm64 ''"
)

for target in "${targets[@]}"; do
    read -r goos goarch suffix <<< "$target"
    name="${MODULE}-${goos}-${goarch}${suffix}"
    out="${OUTDIR}/${name}"
    echo "Building ${name}..."
    env GOOS="$goos" GOARCH="$goarch" go build -o "$out" ./cmd/spacemolt-lt
    echo "  -> ${out}"
done

echo ""
echo "=== Build complete. Output in ./${OUTDIR}/ ==="
