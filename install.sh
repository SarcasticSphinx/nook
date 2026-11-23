#!/bin/sh
# Installs nook as a command you can run from any terminal:
#
#   nook          join the room on this Wi-Fi (or start one)
#   nook host     start the room on this computer
#
# Two ways to run this installer:
#   from the nook folder:  ./install.sh
#   from a running host:   curl -fsSL http://HOST:4747/install.sh | sh
#
# It puts the app in ~/.local/share/nook and links it as ~/.local/bin/nook.
# Only if ~/.local/bin isn't on your PATH yet does it add one line to your
# shell's startup file. Nothing else is installed or changed.
#
# To remove it:  ./install.sh uninstall
#           or:  curl -fsSL http://HOST:4747/install.sh | sh -s uninstall
set -eu

NOOK_BASE="${NOOK_BASE:-}" # the host fills this in when it serves this file

bin="$HOME/.local/bin"
data="$HOME/.local/share/nook"

say() { printf '%s\n' "$*"; }
fail() {
	say "nook: $*" >&2
	exit 1
}

if [ "${1:-}" = "uninstall" ]; then
	rm -f "$bin/nook"
	rm -rf "$data"
	say "nook removed."
	exit 0
fi

os=$(uname -s)
arch=$(uname -m)
osver=0
case "$os" in
Darwin) osver=$(sw_vers -productVersion 2>/dev/null | cut -d. -f1) ;;
Linux) ;;
*) fail "this installer is for Mac and Linux. On Windows, use install.cmd." ;;
esac
case "$osver" in '' | *[!0-9]*) osver=0 ;; esac

mkdir -p "$bin" "$data"
if [ -n "$NOOK_BASE" ]; then
	# Ask the host for the right build for this computer.
	say "downloading nook from $NOOK_BASE ..."
	tmp="$data/.download.$$"
	trap 'rm -f "$tmp"' EXIT
	curl -fsSL -o "$tmp" "$NOOK_BASE/dl/auto?os=$os&arch=$arch&osver=$osver" ||
		fail "download failed. Is the host still running? You can also join in a browser: $NOOK_BASE"
	mv -f "$tmp" "$data/nook"
	trap - EXIT
	target="$data/nook"
else
	# Copy every build from dist/ so this computer can hand the app out too.
	src="$(cd "$(dirname "$0")" && pwd)/dist"
	[ -d "$src" ] || fail "there's no dist/ folder next to this script (run ./build.sh first)"
	if [ "$os" = Darwin ]; then
		a=intel
		if [ "$arch" = arm64 ]; then a=arm64; fi
		if [ "$osver" -eq 0 ] || [ "$osver" -ge 13 ]; then
			list="nook-mac nook-mac-$a nook-mac-legacy"
		else
			list="nook-mac-legacy"
		fi
	else
		list="nook-linux-x64"
	fi
	pick=""
	for f in $list; do
		if [ -f "$src/$f" ]; then
			pick=$f
			break
		fi
	done
	[ -n "$pick" ] || fail "dist/ has no build for this computer ($os $arch)"
	say "copying the app, plus the builds for everyone else ..."
	rm -f "$data/nook"
	for f in "$src"/nook-*; do cp -f "$f" "$data/"; done
	target="$data/$pick"
fi
chmod +x "$data"/nook* 2>/dev/null || true
# Files that arrived by AirDrop or a browser carry a "downloaded from the
# internet" flag that stops unsigned apps from starting. Clear it on the
# nook files only, and say so.
if [ "$os" = Darwin ] && xattr -r "$data" 2>/dev/null | grep -q com.apple.quarantine; then
	xattr -dr com.apple.quarantine "$data" 2>/dev/null || true
	say "cleared macOS's downloaded-file flag on the nook files so they can start"
fi
if ! "$target" version >/dev/null 2>&1; then
	fail "the app doesn't run on this computer${NOOK_BASE:+. Join in a browser instead: $NOOK_BASE}"
fi
ln -sf "$target" "$bin/nook"

case ":$PATH:" in
*":$bin:"*) onpath=yes ;;
*) onpath=no ;;
esac
if [ "$onpath" = no ]; then
	pathline='export PATH="$HOME/.local/bin:$PATH"'
	case "$(basename "${SHELL:-sh}")" in
	zsh) rc="$HOME/.zshrc" ;;
	bash)
		if [ "$os" != Darwin ]; then
			rc="$HOME/.bashrc"
		elif [ -f "$HOME/.bash_profile" ] || [ ! -f "$HOME/.profile" ]; then
			rc="$HOME/.bash_profile"
		else
			rc="$HOME/.profile"
		fi
		;;
	fish)
		rc="$HOME/.config/fish/conf.d/nook.fish"
		pathline='set -gx PATH $HOME/.local/bin $PATH'
		;;
	*) rc="$HOME/.profile" ;;
	esac
	mkdir -p "$(dirname "$rc")"
	if ! grep -qsF "$pathline" "$rc"; then
		printf '\n# added by the nook installer\n%s\n' "$pathline" >>"$rc"
		say "added ~/.local/bin to your PATH in $rc"
	fi
fi

say ""
say "installed $("$target" version | cut -d' ' -f1-2)"
if [ "$onpath" = yes ]; then
	say "start it any time by typing:"
else
	say "open a new terminal window, then type:"
fi
say "  nook         join the room on this Wi-Fi, or start one"
say "  nook host    start the room on this computer"
say "  nook help    everything else"
