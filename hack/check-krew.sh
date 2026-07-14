#!/bin/sh
set -eu
template=${1:-.krew.yaml.tpl}
yq=${2:-yq}
tmp=$(mktemp "${TMPDIR:-/tmp}/kvdrain-krew.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
awk '
  /{{ addURIAndSha / {
    if ($0 !~ /[|] indent 6 }}/) exit 2
    indent="      "
    uri=$0; sub(/^[^"]*"/, "", uri); sub(/".*/, "", uri)
    gsub(/{{ \.TagName }}/, "v0.0.0", uri)
    print indent "uri: " uri
    print indent "sha256: 0000000000000000000000000000000000000000000000000000000000000000"
    next
  }
  { gsub(/{{ \.TagName }}/, "v0.0.0"); print }
' "$template" > "$tmp"
"$yq" eval '.' "$tmp" >/dev/null
[ "$("$yq" eval '.spec.platforms | length' "$tmp")" -eq 6 ]
for pair in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    os=${pair%/*}; arch=${pair#*/}
    [ "$("$yq" eval '[.spec.platforms[] | select(.selector.matchLabels.os == "'"$os"'" and .selector.matchLabels.arch == "'"$arch"'")] | length' "$tmp")" -eq 1 ]
done
grep -q 'kvdrain_v0.0.0_linux_amd64.tar.gz' "$tmp"
grep -q 'kvdrain_v0.0.0_windows_arm64.zip' "$tmp"
printf 'Krew template checks passed\n'
