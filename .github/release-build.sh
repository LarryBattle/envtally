#!/bin/sh
# Builds the release archives for every OS/arch into dist/, plus checksums.txt.
# Needs only Go, tar, zip and sha256sum. Usage: sh .github/release-build.sh [version]
set -eu

version="${1:-dev}"
version="${version#v}" # tag v1.2.3 -> 1.2.3
commit="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
extras="README.md LICENSE envtally.schema.json envtally.example.json envtally.example.full.json examples"

rm -rf dist
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  name="envtally_${version}_${os}_${arch}"
  bin="envtally"
  if [ "$os" = windows ]; then
    bin="envtally.exe"
  fi

  mkdir -p "dist/$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$date" \
    -o "dist/$name/$bin" ./cmd/envtally
  # shellcheck disable=SC2086 # the list is split on purpose
  cp -r $extras "dist/$name/"

  if [ "$os" = windows ]; then
    (cd "dist/$name" && zip -qr "../$name.zip" .)
  else
    tar -C "dist/$name" -czf "dist/$name.tar.gz" .
  fi
  rm -rf "dist/$name"
  echo "built $name"
done

(cd dist && sha256sum envtally_* > checksums.txt)
