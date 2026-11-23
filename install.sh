#!/bin/sh
# Installs nook so you can type `nook` in any terminal. Nothing else is
# needed first: no Go, no Homebrew, no admin password.
#
#   curl -fsSL https://github.com/SarcasticSphinx/nook/releases/latest/download/install.sh | sh
#
# It also works with no internet at all:
#   from a nook folder (USB stick):    ./install.sh   (or double-click install.command)
#   from a nook running in Wi-Fi mode: curl -fsSL http://HOST:4747/install.sh | sh
#
# It puts the app in ~/.local/share/nook and links it as ~/.local/bin/nook.
# Only if ~/.local/bin isn't on your PATH yet does it add one line to your
# shell's startup file. To remove it: sh install.sh uninstall
set -eu

NOOK_BASE="${NOOK_BASE:-}" # the host fills this in when it serves this file
RELEASES="https://github.com/SarcasticSphinx/nook/releases/latest/download"

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
*) fail "this installer is for Mac and Linux. On Windows, double-click install.cmd." ;;
esac
case "$osver" in '' | *[!0-9]*) osver=0 ;; esac

# The ready-made build this computer needs.
asset=""
if [ "$os" = Darwin ]; then
	if [ "$osver" -eq 0 ] || [ "$osver" -ge 13 ]; then asset=nook-mac; else asset=nook-mac-legacy; fi
elif [ "$arch" = x86_64 ] || [ "$arch" = amd64 ]; then
	asset=nook-linux-x64
fi

fetch() { # URL FILE
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		fail "this computer has neither curl nor wget to download with"
	fi
}

# Run from a nook folder with builds in dist/ (a USB copy, or ./build.sh)?
src=""
case "$0" in
*install.sh | *install.command)
	d=$(cd "$(dirname "$0")" 2>/dev/null && pwd || true)
	if [ -n "$d" ] && [ -d "$d/dist" ]; then src="$d/dist"; fi
	;;
esac

mkdir -p "$bin" "$data"
tmp="$data/.download.$$"
trap 'rm -f "$tmp" "$data/.copy.$$"' EXIT
if [ -n "$NOOK_BASE" ]; then
	say "downloading nook from $NOOK_BASE ..."
	fetch "$NOOK_BASE/dl/auto?os=$os&arch=$arch&osver=$osver" "$tmp" ||
		fail "download failed. Is the host still running? You can also join in a browser: $NOOK_BASE"
	mv -f "$tmp" "$data/nook"
	target="$data/nook"
elif [ -n "$src" ]; then
	a=intel
	if [ "$arch" = arm64 ]; then a=arm64; fi
	pick=""
	for f in "$asset" "nook-mac-$a" nook-mac-legacy nook-linux-x64; do
		case "$os:$f" in
		Darwin:nook-mac* | Linux:nook-linux-x64)
			if [ -f "$src/$f" ]; then
				pick=$f
				break
			fi
			;;
		esac
	done
	[ -n "$pick" ] || fail "the dist folder has no build for this computer ($os $arch)"
	say "copying the app, plus the builds for everyone else ..."
	rm -f "$data/nook"
	for f in "$src"/nook-*; do
		# copy, then rename into place: macOS kills a signed app that was
		# overwritten in place, so an upgrade must never do that
		cp -f "$f" "$data/.copy.$$"
		mv -f "$data/.copy.$$" "$data/$(basename "$f")"
	done
	target="$data/$pick"
else
	[ -n "$asset" ] || fail "there is no ready-made nook for $os on $arch yet"
	say "downloading nook from GitHub ..."
	fetch "$RELEASES/$asset" "$tmp" ||
		fail "download failed. Check the internet connection, or install from a USB copy of nook."
	mv -f "$tmp" "$data/nook"
	target="$data/nook"
fi
trap - EXIT
chmod +x "$data"/nook* 2>/dev/null || true
# Files that came by AirDrop or a browser carry a "downloaded from the
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
say "  nook         chat over Bluetooth with the laptops nearby"
say "  nook wifi    chat over a shared Wi-Fi network instead"
say "  nook help    everything else"
