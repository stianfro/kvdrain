#!/bin/sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/kvdrain-install-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
release="$tmp/releases/download/v1.2.3"
mkdir -p "$release" "$tmp/payload" "$tmp/bin" "$tmp/fakebin"
archive="$tmp/kvdrain-fixture.tar.gz"
platforms="linux_amd64 linux_arm64 darwin_amd64 darwin_arm64"

make_archive() {
    reported=$1
    cat > "$tmp/payload/kvdrain" <<BIN
#!/bin/sh
[ "\${1:-}" = version ] || exit 2
printf 'kvdrain $reported (commit test, built test)\n'
BIN
    chmod +x "$tmp/payload/kvdrain"
    tar -czf "$archive" -C "$tmp/payload" kvdrain
    : > "$release/checksums.txt"
    for platform in $platforms; do
        target="$release/kvdrain_v1.2.3_${platform}.tar.gz"
        cp "$archive" "$target"
        if command -v sha256sum >/dev/null 2>&1; then hash=$(sha256sum "$target" | awk '{print $1}'); else hash=$(shasum -a 256 "$target" | awk '{print $1}'); fi
        printf '%s  %s\n' "$hash" "kvdrain_v1.2.3_${platform}.tar.gz" >> "$release/checksums.txt"
    done
}

make_archive 1.2.3
printf '{"tag_name":"v1.2.3","draft":false,"prerelease":false}\n' > "$tmp/latest.json"

cat > "$tmp/fakebin/uname" <<'FAKE_UNAME'
#!/bin/sh
case "${1:-}" in
    -s) printf '%s\n' "${KVDRAIN_FIXTURE_OS:-Linux}" ;;
    -m) printf '%s\n' "${KVDRAIN_FIXTURE_ARCH:-x86_64}" ;;
    *) exit 2 ;;
esac
FAKE_UNAME
cat > "$tmp/fakebin/curl" <<'FAKE_CURL'
#!/bin/sh
set -eu
output=""
url=""
secure_proto=0
secure_redirect=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output=$2; shift 2 ;;
        --proto) [ "$2" = '=https' ] || exit 2; secure_proto=1; shift 2 ;;
        --proto-redir) [ "$2" = '=https' ] || exit 2; secure_redirect=1; shift 2 ;;
        -*) shift ;;
        *) url=$1; shift ;;
    esac
done
[ "$secure_proto" -eq 1 ] && [ "$secure_redirect" -eq 1 ] || exit 2
case "$url" in
    https://api.github.com/repos/stianfro/kvdrain/releases/latest)
        source=$KVDRAIN_FIXTURE_ROOT/latest.json
        ;;
    https://github.com/stianfro/kvdrain/releases/download/*)
        relative=${url#https://github.com/stianfro/kvdrain/releases/download/}
        source=$KVDRAIN_FIXTURE_ROOT/releases/download/$relative
        ;;
    *)
        printf 'unexpected URL: %s\n' "$url" >&2
        exit 2
        ;;
esac
if [ -n "$output" ]; then cp "$source" "$output"; else cat "$source"; fi
FAKE_CURL
chmod +x "$tmp/fakebin/uname" "$tmp/fakebin/curl"

run_install() {
    PATH="$tmp/fakebin:$PATH" KVDRAIN_FIXTURE_ROOT="$tmp" \
      KVDRAIN_INSTALL_DIR="$tmp/bin" "$root/install.sh"
}
output=$(run_install 2>&1)
printf '%s\n' "$output" | grep -q 'Installed kvdrain v1.2.3'
printf '%s\n' "$output" | grep -q 'is not on PATH'
[ "$("$tmp/bin/kvdrain" version | awk '{print $2}')" = 1.2.3 ]
for fixture in 'Linux aarch64' 'Darwin x86_64' 'Darwin arm64'; do
    fixture_os=${fixture% *}
    fixture_arch=${fixture#* }
    rm -f "$tmp/bin/kvdrain"
    KVDRAIN_FIXTURE_OS=$fixture_os KVDRAIN_FIXTURE_ARCH=$fixture_arch KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1
    [ "$("$tmp/bin/kvdrain" version | awk '{print $2}')" = 1.2.3 ]
done
mkdir "$tmp/fallbackbin"
cp "$tmp/fakebin/uname" "$tmp/fakebin/curl" "$tmp/fallbackbin/"
for command_name in awk sed head grep mktemp rm tar gzip mkdir chmod cp cat mv shasum; do
    ln -s "$(command -v "$command_name")" "$tmp/fallbackbin/$command_name"
done
rm -f "$tmp/bin/kvdrain"
PATH="$tmp/fallbackbin" KVDRAIN_FIXTURE_ROOT="$tmp" KVDRAIN_INSTALL_DIR="$tmp/bin" \
  KVDRAIN_VERSION=v1.2.3 "$root/install.sh" >/dev/null 2>&1
[ "$("$tmp/bin/kvdrain" version | awk '{print $2}')" = 1.2.3 ]
KVDRAIN_VERSION=v1.2.3 run_install >/dev/null
if KVDRAIN_VERSION=1.2.3 run_install >/dev/null 2>&1; then
    echo "malformed version unexpectedly accepted" >&2; exit 1
fi
if KVDRAIN_FIXTURE_ARCH=i686 KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1; then
    echo "unsupported architecture unexpectedly accepted" >&2; exit 1
fi
printf 'old\n' > "$tmp/bin/kvdrain"
printf '%s  %s\n' '0000000000000000000000000000000000000000000000000000000000000000' kvdrain_v1.2.3_linux_amd64.tar.gz > "$release/checksums.txt"
if KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1; then
    echo "corrupt checksum unexpectedly installed" >&2; exit 1
fi
[ "$(cat "$tmp/bin/kvdrain")" = old ]
rm -f "$tmp/bin/kvdrain"
mkdir "$tmp/bin/kvdrain"
if KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1; then
    echo "directory destination unexpectedly accepted" >&2; exit 1
fi
[ -d "$tmp/bin/kvdrain" ]
rmdir "$tmp/bin/kvdrain"
printf 'old\n' > "$tmp/bin/kvdrain"
make_archive 1.2.3
cat "$release/checksums.txt" >> "$release/checksums.txt.duplicate"
cat "$release/checksums.txt" >> "$release/checksums.txt.duplicate"
mv "$release/checksums.txt.duplicate" "$release/checksums.txt"
if KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1; then
    echo "duplicate checksum unexpectedly accepted" >&2; exit 1
fi
[ "$(cat "$tmp/bin/kvdrain")" = old ]
make_archive 9.9.9
if KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1; then
    echo "mismatched binary version unexpectedly installed" >&2; exit 1
fi
[ "$(cat "$tmp/bin/kvdrain")" = old ]
mkdir -p "$tmp/unsafe/nested"
cp "$tmp/payload/kvdrain" "$tmp/unsafe/nested/kvdrain"
tar -czf "$release/kvdrain_v1.2.3_linux_amd64.tar.gz" -C "$tmp/unsafe" nested/kvdrain
if command -v sha256sum >/dev/null 2>&1; then
    hash=$(sha256sum "$release/kvdrain_v1.2.3_linux_amd64.tar.gz" | awk '{print $1}')
else
    hash=$(shasum -a 256 "$release/kvdrain_v1.2.3_linux_amd64.tar.gz" | awk '{print $1}')
fi
printf '%s  %s\n' "$hash" kvdrain_v1.2.3_linux_amd64.tar.gz > "$release/checksums.txt"
if KVDRAIN_VERSION=v1.2.3 run_install >/dev/null 2>&1; then
    echo "non-top-level archive member unexpectedly installed" >&2; exit 1
fi
[ "$(cat "$tmp/bin/kvdrain")" = old ]
printf 'install tests passed\n'
