#!/usr/bin/env bash
# Prepare the pinned OmniTunnel payload for embedding in the BackPack binary.
set -euo pipefail

arch="${1:?usage: bash scripts/build-omnitunnel.sh <amd64|arm64>}"
case "$arch" in
    amd64|arm64) ;;
    *) echo "OmniTunnel supports Linux amd64 and arm64" >&2; exit 2 ;;
esac
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
revision=acf4e6e5bacc4aa841320e39cff4b51e3f2a9eec
digest=e6c0385b6edb5199bb4331d48d14a7ee3afb181b0fdccfc25672f322d2ed0747
dest="$root/internal/omni/payload/bundle-$arch.tar.gz"
stage="$(mktemp -d)"
tmp="$(mktemp "$dest.XXXXXX")"
trap 'rm -rf -- "$stage"; rm -f -- "$tmp"' EXIT
source_dir="$stage/source"
payload="$stage/payload"
mkdir -p "$source_dir" "$payload/bin"

url="https://codeload.github.com/Free-Guy-IR/OmniTunnel/tar.gz/$revision"
curl -fsSL --connect-timeout 15 --max-time 180 --retry 2 "$url" -o "$stage/omni.tar.gz"
printf '%s  %s\n' "$digest" "$stage/omni.tar.gz" | sha256sum -c --status
top="OmniTunnel-$revision"
tar -xzf "$stage/omni.tar.gz" -C "$source_dir" --strip-components=1 \
    "$top/LICENSE" "$top/omnitunnel.sh" "$top/src" \
    "$top/bin/hysteria-$arch" "$top/bin/chisel-$arch"
python3 "$root/scripts/adapt-omnitunnel.py" "$source_dir" "$payload/manager"
bash -n "$payload/manager"

read -r -a compiler <<< "${OMNI_CC:-cc}"
"${compiler[@]}" -std=gnu11 -O2 -fstack-protector-strong -D_FORTIFY_SOURCE=2 -pthread \
    -o "$payload/bin/omnitun" \
    "$source_dir"/src/{main,obsctun,obsctcp,obscmux,obscws,icmptun,monocypher}.c
for engine in omnistripe omniackless; do
    (cd "$source_dir/src/$engine" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 "${GO_BIN:-go}" \
        build -trimpath -ldflags '-s -w -buildid=' -o "$payload/bin/$engine" .)
done
for engine in hysteria chisel; do
    install -m 0755 "$source_dir/bin/$engine-$arch" "$payload/bin/$engine"
done
install -m 0644 "$source_dir/LICENSE" "$payload/LICENSE"
chmod 0755 "$payload/manager"
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -czf "$tmp" \
    -C "$payload" manager LICENSE bin/omnitun bin/omnistripe bin/omniackless bin/hysteria bin/chisel
chmod 0644 "$tmp"
mv -f -- "$tmp" "$dest"
