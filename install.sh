#!/bin/sh
set -eu

repo="stianfro/kvdrain"
install_dir=${KVDRAIN_INSTALL_DIR:-${XDG_BIN_HOME:-"$HOME/.local/bin"}}
api_url="https://api.github.com/repos/$repo/releases/latest"
download_base="https://github.com/$repo/releases/download"

fail() { printf 'kvdrain installer: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }

need curl
need tar
if command -v sha256sum >/dev/null 2>&1; then
    hash_file() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    hash_file() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    fail "sha256sum or shasum is required"
fi

os=$(uname -s)
case "$os" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "unsupported operating system: $os" ;;
esac
arch=$(uname -m)
case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) fail "unsupported architecture: $arch" ;;
esac

case "$os/$arch" in linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;; *) fail "unsupported platform: $os/$arch" ;; esac

fetch() {
    curl -fsSL --proto '=https' --proto-redir '=https' "$@"
}

version=${KVDRAIN_VERSION:-}
if [ -z "$version" ]; then
    release_json=$(fetch "$api_url") || fail "could not resolve the latest regular release"
    version=$(printf '%s\n' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
case "$version" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) fail "version must have the form vX.Y.Z" ;;
esac
# Reject suffixes and malformed numeric components.
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail "version must have the form vX.Y.Z"

archive="kvdrain_${version}_${os}_${arch}.tar.gz"
base="$download_base/$version"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/kvdrain-install.XXXXXX") || fail "could not create temporary directory"
staged=""
cleanup() { rm -rf "$tmp"; [ -z "$staged" ] || rm -f "$staged"; }
trap cleanup EXIT HUP INT TERM

fetch -o "$tmp/$archive" "$base/$archive" || fail "could not download $archive"
fetch -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download checksums.txt"

matches=$(awk -v name="$archive" '$2 == name && NF == 2 {print $1}' "$tmp/checksums.txt")
[ "$(printf '%s\n' "$matches" | awk 'NF {n++} END {print n+0}')" -eq 1 ] || fail "checksums.txt must contain exactly one entry for $archive"
expected=$(printf '%s\n' "$matches")
printf '%s\n' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$' || fail "invalid checksum entry for $archive"
actual=$(hash_file "$tmp/$archive")
[ "$actual" = "$expected" ] || fail "checksum mismatch for $archive"

mkdir "$tmp/extract"
[ "$(tar -tzf "$tmp/$archive" | awk '$0 == "kvdrain" {n++} END {print n+0}')" -eq 1 ] || fail "archive does not contain exactly one top-level kvdrain binary"
tar -xzf "$tmp/$archive" -C "$tmp/extract" kvdrain
[ -f "$tmp/extract/kvdrain" ] && [ ! -L "$tmp/extract/kvdrain" ] || fail "extracted kvdrain is not a regular file"
chmod 0755 "$tmp/extract/kvdrain"

mkdir -p "$install_dir"
[ -d "$install_dir" ] || fail "install destination is not a directory: $install_dir"
destination="$install_dir/kvdrain"
[ ! -d "$destination" ] || fail "install destination is a directory: $destination"
if [ -e "$destination" ] && [ ! -f "$destination" ] && [ ! -L "$destination" ]; then
    fail "install destination is not a regular file or symlink: $destination"
fi
staged=$(mktemp "$install_dir/.kvdrain.tmp.XXXXXX") || fail "could not stage installation in $install_dir"
cp "$tmp/extract/kvdrain" "$staged"
chmod 0755 "$staged"
reported=$($staged version 2>/dev/null | awk 'NR == 1 {print $2}') || fail "downloaded binary could not report its version"
case "$reported" in "$version"|"${version#v}") ;; *) fail "downloaded binary reported version $reported, expected $version" ;; esac
mv -f "$staged" "$destination"
staged=""

printf 'Installed kvdrain %s to %s/kvdrain\n' "$version" "$install_dir"
case ":${PATH:-}:" in *":$install_dir:"*) ;; *) printf 'Warning: %s is not on PATH. Add it in your shell configuration.\n' "$install_dir" >&2 ;; esac
