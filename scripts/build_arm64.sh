#!/bin/bash
# Local build script for OpsIntelligence linux-arm64 using Zig
# Requires Zig installed: brew install zig

set -e

echo "[opsintelligence] Building for linux-arm64 using Zig..."

# Create dist directory
mkdir -p dist

# Set CGO flags and compilers
export CGO_ENABLED=1
export GOOS=linux
export GOARCH=arm64
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"

# Build with static linking and fts5 tags
go build -mod=vendor -tags fts5 -ldflags "-s -w -extldflags '-static'" -o dist/opsintelligence-linux-arm64 ./cmd/opsintelligence

echo "[opsintelligence] Build complete: dist/opsintelligence-linux-arm64"
ls -lh dist/opsintelligence-linux-arm64
file dist/opsintelligence-linux-arm64
