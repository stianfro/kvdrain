#!/bin/sh
set -eu

version=v0.0.51
sha256=3748407285b4cf866e9d4625e376aca927aa3f0b30f30ede83cc33a11566f28b
asset="krew-release-bot_${version}_linux_amd64.tar.gz"
url="https://github.com/rajatjindal/krew-release-bot/releases/download/${version}/${asset}"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/kvdrain-krew.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

curl -fsSL --proto '=https' --proto-redir '=https' -o "$tmp/$asset" "$url"
if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
    actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
[ "$actual" = "$sha256" ] || {
    printf 'krew-release-bot checksum mismatch\n' >&2
    exit 1
}
tar -xzf "$tmp/$asset" -C "$tmp" krew-release-bot
chmod 0755 "$tmp/krew-release-bot"
INPUT_KREW_TEMPLATE_FILE=.krew.yaml.tpl "$tmp/krew-release-bot" action
