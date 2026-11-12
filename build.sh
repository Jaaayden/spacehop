#!/bin/sh
# Builds the Windows executables from any OS with Go installed.
# The version comes from the VERSION file unless given: ./build.sh 1.2.3
set -eu
cd "$(dirname "$0")"
VERSION="${1:-$(cat VERSION)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
go run ./tools/mkicon assets
go run ./tools/mkres "$VERSION"
mkdir -p dist
for arch in amd64 arm64; do
  GOOS=windows GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
    -ldflags "-H=windowsgui -s -w -X main.version=$VERSION+$COMMIT" \
    -o "dist/SpaceHop-$arch.exe" .
done
(cd dist && shasum -a 256 SpaceHop-*.exe > SHA256SUMS.txt)
ls -l dist
