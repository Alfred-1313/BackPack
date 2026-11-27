#!/usr/bin/env bash
# Build the pinned historical source with a verified-TLS forwarding entry point.
set -euo pipefail
archive="$1"
output="$2"
toolchain="$3"
assets="$(cd "$(dirname "$0")" && pwd)"
source_dir="$(dirname "$archive")/gost-v1-source"
mkdir "$source_dir"
tar -xzf "$archive" -C "$source_dir" --strip-components=1
cd "$source_dir"
sed -i 's#code.google.com/p/go-uuid/uuid#github.com/pborman/uuid#' http.go
sed -i '/flag.Parse()/i\	flag.StringVar(\&backpackConfig, "C", "", "BackPack forwarding config")' main.go
sed -i '/^func main() {/a\	if backpackConfig != "" {\n		log.Fatal(runBackpack(backpackConfig))\n	}' main.go
cp "$assets/go.mod" "$assets/go.sum" "$assets/backpack.go" .
GOTOOLCHAIN=local CGO_ENABLED=0 "$toolchain" build -mod=readonly -trimpath -ldflags='-s -w' -o "$output" .
cp LICENSE "$(dirname "$output")/LICENSE"
