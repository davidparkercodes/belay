#!/bin/sh
# Install the latest Belay release (or BELAY_VERSION) into BELAY_INSTALL_DIR.
#   curl -fsSL https://raw.githubusercontent.com/davidparkercodes/belay/main/install.sh | sh
set -eu

REPO="davidparkercodes/belay"
INSTALL_DIR="${BELAY_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${BELAY_VERSION:-}"

say() { printf '%s\n' "$*"; }
fail() { printf 'belay install: %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		fail "curl or wget is required"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		fail "sha256sum, shasum or openssl is required to verify the download"
	fi
}

need uname
need tar
need awk

case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported OS $(uname -s); see https://github.com/$REPO/releases" ;;
esac

case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported architecture $(uname -m); see https://github.com/$REPO/releases" ;;
esac

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t belay-install)"
staged=""
cleanup() {
	rm -rf "$tmp"
	if [ -n "$staged" ]; then rm -f "$staged"; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM

if [ -z "$VERSION" ]; then
	fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest.json" ||
		fail "could not query the latest release"
	VERSION="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)"
	[ -n "$VERSION" ] || fail "could not read the latest release tag"
fi
case "$VERSION" in
	v*) ;;
	*) VERSION="v$VERSION" ;;
esac

asset="belay_${VERSION#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

say "Downloading Belay $VERSION for $os/$arch..."
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download checksums.txt for $VERSION"
fetch "$base/$asset" "$tmp/$asset" || fail "could not download $asset"

want="$(awk -v f="$asset" '$2 == f || $2 == "*" f {print $1}' "$tmp/checksums.txt" | head -n 1)"
[ -n "$want" ] || fail "checksums.txt has no entry for $asset; refusing to install"
got="$(sha256_of "$tmp/$asset")"
[ "$want" = "$got" ] || fail "checksum mismatch for $asset (expected $want, got $got); refusing to install"

mkdir -p "$tmp/extract"
tar -xzf "$tmp/$asset" -C "$tmp/extract" belay || fail "archive does not contain a belay binary"

if ! mkdir -p "$INSTALL_DIR" 2>/dev/null || [ ! -w "$INSTALL_DIR" ]; then
	fail "cannot write to $INSTALL_DIR; set BELAY_INSTALL_DIR to a directory you own"
fi

staged="$INSTALL_DIR/.belay-install.$$"
cp "$tmp/extract/belay" "$staged"
chmod 0755 "$staged"
if [ -f "$INSTALL_DIR/belay" ]; then
	cp -p "$INSTALL_DIR/belay" "$INSTALL_DIR/belay.prev" 2>/dev/null || true
fi
mv -f "$staged" "$INSTALL_DIR/belay"
staged=""

say "Installed Belay $VERSION to $INSTALL_DIR/belay"

case ":${PATH:-}:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		say ""
		say "$INSTALL_DIR is not on your PATH. Add it to your shell profile:"
		say "  export PATH=\"$INSTALL_DIR:\$PATH\""
		;;
esac

say ""
say "Next: cd into a project and run 'belay init'. Keep it current with 'belay update'."
