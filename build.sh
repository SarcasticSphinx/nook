#!/bin/sh
# Builds nook for every platform into dist/.
#
#   ./build.sh
#
# Needs Go 1.25 or newer (https://go.dev/dl). Mac builds get Bluetooth only
# when built on a Mac with Apple's command line tools (xcode-select --install);
# built anywhere else they are Wi-Fi only. Windows and Linux builds always
# include Bluetooth.
#
# For old computers (Windows 7/8, macOS 10.13 to 12) also point GO_LEGACY at
# a Go 1.20 toolchain, the last one that supports them:
#
#   GO_LEGACY=/path/to/go1.20.14/bin/go ./build.sh
set -eu
cd "$(dirname "$0")"

GO=${GO:-go}
VERSION=${VERSION:-$(sed -n 's/^var version = "\(.*\)"/\1/p' main.go)}
LDFLAGS="-s -w -X main.version=$VERSION"
OUT=dist
ID=io.github.sarcasticsphinx.nook
mkdir -p "$OUT"

size() { printf '%6s KB\n' "$(($(wc -c <"$OUT/$1") / 1024))"; }

# build TOOLCHAIN GOOS GOARCH OUTPUT [go build flags...]
build() {
	g=$1 os=$2 arch=$3 out=$4
	shift 4
	printf '  %-22s' "$out"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$g" build -trimpath -ldflags "$LDFLAGS" "$@" -o "$OUT/$out" .
	size "$out"
}

# mac TOOLCHAIN GOARCH OUTPUT OLDEST_MACOS [go build flags...]
mac() {
	g=$1 arch=$2 out=$3 min=$4
	shift 4
	printf '  %-22s' "$out"
	if [ "$(uname -s)" = Darwin ] && command -v clang >/dev/null 2>&1; then
		cc=$arch
		if [ "$arch" = amd64 ]; then cc=x86_64; fi
		CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $cc" MACOSX_DEPLOYMENT_TARGET=$min \
			"$g" build -trimpath -ldflags "$LDFLAGS" "$@" -o "$OUT/$out" .
	else
		CGO_ENABLED=0 GOOS=darwin GOARCH=$arch "$g" build -trimpath -ldflags "$LDFLAGS" "$@" -o "$OUT/$out" .
	fi
	size "$out"
}

# Signing ties the embedded Bluetooth description to the app, so macOS asks
# for permission once and remembers it.
sign() {
	if command -v codesign >/dev/null 2>&1; then codesign -s - -f --identifier "$ID" "$OUT/$1" 2>/dev/null || true; fi
}

# universal ARM64 INTEL OUTPUT: one file for Apple Silicon and Intel Macs.
universal() {
	if command -v lipo >/dev/null 2>&1; then
		lipo -create -output "$OUT/$3" "$OUT/$1" "$OUT/$2"
		rm -f "$OUT/$1" "$OUT/$2"
		sign "$3"
		printf '  %-22s%6s KB (universal)\n' "$3" "$(($(wc -c <"$OUT/$3") / 1024))"
	fi
}

echo "nook $VERSION with $("$GO" version | cut -d' ' -f3)"
mac "$GO" arm64 nook-mac-arm64 13.0
mac "$GO" amd64 nook-mac-intel 13.0
universal nook-mac-arm64 nook-mac-intel nook-mac
build "$GO" windows amd64 nook-win-x64.exe
build "$GO" windows arm64 nook-win-arm64.exe
build "$GO" windows 386 nook-win-x86.exe
build "$GO" linux amd64 nook-linux-x64

if [ -n "${GO_LEGACY:-}" ]; then
	# Old systems need old Go, and old Go needs the old libraries
	# (go.legacy.mod). Windows 7/8 have no modern Bluetooth: Wi-Fi only.
	echo "legacy builds with $("$GO_LEGACY" version | cut -d' ' -f3)"
	mac "$GO_LEGACY" arm64 .legacy-arm64 11.0 -modfile=go.legacy.mod
	mac "$GO_LEGACY" amd64 .legacy-amd64 10.13 -modfile=go.legacy.mod
	universal .legacy-arm64 .legacy-amd64 nook-mac-legacy
	if [ -f "$OUT/.legacy-amd64" ]; then # no lipo here: ship the Intel build
		mv "$OUT/.legacy-amd64" "$OUT/nook-mac-legacy"
		rm -f "$OUT/.legacy-arm64"
	fi
	build "$GO_LEGACY" windows amd64 nook-win7-x64.exe -modfile=go.legacy.mod -tags nobluetooth
	build "$GO_LEGACY" windows 386 nook-win7-x86.exe -modfile=go.legacy.mod -tags nobluetooth
fi
for f in nook-mac nook-mac-arm64 nook-mac-intel; do
	if [ -f "$OUT/$f" ]; then sign "$f"; fi
done
echo "done: $OUT/"
