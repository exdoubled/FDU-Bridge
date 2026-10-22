#!/usr/bin/env bash
# Builds FDU Connect on Linux or macOS; the Windows counterpart is build.ps1.
# Usage: gui/build.sh [--test] [--package] [--output DIR] [--version vX.Y.Z]
set -euo pipefail

wails_version=v3.0.0-beta.34
here="$(cd "$(dirname "$0")" && pwd)"
root="$(dirname "$here")"
test=0 package=0 output="$root/dist" version=dev
while [ $# -gt 0 ]; do
  case "$1" in
    --test) test=1 ;;
    --package) package=1 ;;
    --output) output="$2"; shift ;;
    --version) version="$2"; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done
if ! [[ "$version" =~ ^(dev|v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$ ]]; then
  echo "version must be dev or vX.Y.Z[-suffix]" >&2
  exit 2
fi
mkdir -p "$output"
output="$(cd "$output" && pwd)"
plain_version="${version#v}"
# macOS bundle metadata requires a numeric dotted version, without SemVer suffixes.
plain_version="${plain_version%%-*}"
if [ "$version" = dev ]; then plain_version="0.0.0"; fi

wails="$(go env GOPATH)/bin/wails3"
if command -v wails3 >/dev/null && [ "$(wails3 version 2>/dev/null)" = "$wails_version" ]; then
  wails="$(command -v wails3)"
fi
[ -x "$wails" ] || go install "github.com/wailsapp/wails/v3/cmd/wails3@$wails_version"

cd "$here"
# Go embeds this directory while generating bindings, before the first Vite build.
mkdir -p frontend/dist
[ -n "$(ls -A frontend/dist)" ] || touch frontend/dist/.gitkeep
# Bindings first: the frontend type-checks against them.
"$wails" generate bindings -silent -ts -d frontend/bindings ./...
(
  cd frontend
  pnpm install --frozen-lockfile
  if [ "$test" = 1 ]; then pnpm test; fi
  pnpm build
)
if [ "$test" = 1 ]; then go test ./...; fi

core_flags="-X main.zjuConnectVersion=$version"
case "$(uname -s)" in
  Linux)
    arch="$(go env GOARCH)"
    go build -tags production -trimpath -ldflags "-s -w" -o "$output/fdu-connect-gui" .
    (cd "$root" && CGO_ENABLED=0 go build -trimpath -ldflags "$core_flags" -o "$output/fdu-connect" .)
    cp "$root/config.toml.example" "$output/"
    echo "$output/fdu-connect-gui"
    if [ "$package" = 1 ]; then
      stage="$(mktemp -d)"
      dir="$stage/FDU-Connect"
      mkdir -p "$dir"
      # Package public files explicitly; never archive the user's output directory.
      cp "$output/fdu-connect-gui" "$output/fdu-connect" "$root/config.toml.example" "$root/LICENSE" \
        build/linux/fdu-connect.desktop build/linux/fdu-connect.png "$dir/"
      name="FDU-Connect-linux-$arch.tar.gz"
      if [ "$version" != dev ]; then name="FDU-Connect-$version-linux-$arch.tar.gz"; fi
      tar -C "$stage" -czf "$output/$name" FDU-Connect
      rm -rf "$stage"
      echo "$output/$name"
    fi
    ;;
  Darwin)
    # One universal bundle for Apple silicon and Intel Macs.
    work="$(mktemp -d)"
    for arch in arm64 amd64; do
      CGO_ENABLED=1 GOARCH="$arch" go build -tags production -trimpath -ldflags "-s -w" -o "$work/gui-$arch" .
      (cd "$root" && CGO_ENABLED=0 GOARCH="$arch" go build -trimpath -ldflags "$core_flags" -o "$work/core-$arch" .)
    done
    app="$output/FDU Connect.app"
    rm -rf "$app"
    mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
    lipo -create -output "$app/Contents/MacOS/FDU Connect" "$work/gui-arm64" "$work/gui-amd64"
    lipo -create -output "$app/Contents/MacOS/fdu-connect" "$work/core-arm64" "$work/core-amd64"
    rm -rf "$work"
    cp build/darwin/icons.icns "$root/config.toml.example" "$root/LICENSE" "$app/Contents/Resources/"
    sed "s/@VERSION@/$plain_version/g" build/darwin/Info.plist > "$app/Contents/Info.plist"
    # Ad-hoc signature: required for arm64 code, not a Developer ID.
    codesign --force --deep --sign - "$app"
    echo "$app"
    if [ "$package" = 1 ]; then
      name="FDU-Connect-macOS-universal.zip"
      if [ "$version" != dev ]; then name="FDU-Connect-$version-macOS-universal.zip"; fi
      rm -f "$output/$name"
      ditto -c -k --sequesterRsrc --keepParent "$app" "$output/$name"
      echo "$output/$name"
    fi
    ;;
  *)
    echo "build.sh supports Linux and macOS; use build.ps1 on Windows" >&2
    exit 1
    ;;
esac
