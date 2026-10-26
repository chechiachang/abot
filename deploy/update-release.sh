#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: $0 destination-directory" >&2
	exit 2
fi

for command in curl jq tar sha256sum awk; do
	command -v "$command" >/dev/null || { echo "missing required command: $command" >&2; exit 1; }
done

[[ "$(uname -s)" == Linux ]] || { echo "Linux is required" >&2; exit 1; }
case "$(uname -m)" in
	x86_64|amd64) arch=amd64 ;;
	aarch64|arm64) arch=arm64 ;;
	*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

destination=$1
mkdir -p "$destination"
release=$(curl -fsSL --retry 3 https://api.github.com/repos/chechiachang/abot/releases/latest)
tag=$(jq -er '.tag_name | strings | select(test("^v[0-9]+(\\.[0-9]+){2}([.-][0-9A-Za-z.-]+)?$"))' <<<"$release")
version=${tag#v}
asset="abot_${version}_linux_${arch}.tar.gz"
base="https://github.com/chechiachang/abot/releases/download/${tag}"
tmp=$(mktemp -d "${destination}/.abot-release.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --retry 3 "$base/$asset" -o "$tmp/$asset"
curl -fsSL --retry 3 "$base/checksums.txt" -o "$tmp/checksums.txt"
expected=$(awk -v asset="$asset" '$2 == asset { print $1; count++ } END { if (count != 1) exit 1 }' "$tmp/checksums.txt") || {
	echo "checksum entry missing or duplicated for $asset" >&2
	exit 1
}
[[ "$expected" =~ ^[[:xdigit:]]{64}$ ]] || { echo "invalid checksum for $asset" >&2; exit 1; }
actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
[[ "$actual" == "$expected" ]] || { echo "checksum mismatch for $asset" >&2; exit 1; }

tar -xzf "$tmp/$asset" -C "$tmp" abot
chmod 0755 "$tmp/abot"
mv -f "$tmp/abot" "$destination/abot"
echo "installed abot $tag ($arch)"
