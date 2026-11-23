#!/bin/sh
# Builds nook for every platform into dist/. Run it on any computer with Go
# installed (https://go.dev/dl): nothing else is needed, no C compiler.
#
#   ./build.sh
#
# For old computers (Windows 7/8, macOS 10.13-12) also point GO_LEGACY at a
# Go 1.20 toolchain, the last one that supports them:
#
#   GO_LEGACY=/path/to/go1.20.14/bin/go ./build.sh
set -eu
cd "$(dirname "$0")"

GO=${GO:-go}
VERSION=${VERSION:-$(sed -n 's/^var version = "\(.*\)"/\1/p' main.go)}
LDFLAGS="-s -w -X main.version=$VERSION"
OUT=dist
mkdir -p "$OUT"

build() { # toolchain goos goarch output
	printf '  %-22s' "$4"
	CGO_ENABLED=0 GOOS=$2 GOARCH=$3 "$1" build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$4" .
	printf '%6s KB\n' "$(($(wc -c <"$OUT/$4") / 1024))"
}

# Join two Mac builds into one file that runs on Apple Silicon and Intel.
universal() { # arm64-file amd64-file output
	if command -v lipo >/dev/null 2>&1; then
		lipo -create -output "$OUT/$3" "$OUT/$1" "$OUT/$2"
		rm -f "$OUT/$1" "$OUT/$2"
		command -v codesign >/dev/null 2>&1 && codesign -s - -f "$OUT/$3" 2>/dev/null || true
		printf '  %-22s%6s KB (universal)\n' "$3" "$(($(wc -c <"$OUT/$3") / 1024))"
	fi
}

echo "nook $VERSION with $("$GO" version | cut -d' ' -f3)"
build "$GO" darwin arm64 nook-mac-arm64
build "$GO" darwin amd64 nook-mac-intel
universal nook-mac-arm64 nook-mac-intel nook-mac
build "$GO" windows amd64 nook-win-x64.exe
build "$GO" windows arm64 nook-win-arm64.exe
build "$GO" windows 386 nook-win-x86.exe
build "$GO" linux amd64 nook-linux-x64

if [ -n "${GO_LEGACY:-}" ]; then
	echo "legacy builds with $("$GO_LEGACY" version | cut -d' ' -f3)"
	build "$GO_LEGACY" darwin arm64 .legacy-arm64
	build "$GO_LEGACY" darwin amd64 .legacy-amd64
	universal .legacy-arm64 .legacy-amd64 nook-mac-legacy
	if [ -f "$OUT/.legacy-amd64" ]; then # no lipo here: ship the Intel build
		mv "$OUT/.legacy-amd64" "$OUT/nook-mac-legacy"
		rm -f "$OUT/.legacy-arm64"
	fi
	build "$GO_LEGACY" windows amd64 nook-win7-x64.exe
	build "$GO_LEGACY" windows 386 nook-win7-x86.exe
fi
echo "done: $OUT/"
