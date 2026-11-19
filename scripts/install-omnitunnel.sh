#!/usr/bin/env bash
# Loaded by install.sh after source-build dependencies are ready.
install_omnitunnel() (
    set -euo pipefail
    local revision=acf4e6e5bacc4aa841320e39cff4b51e3f2a9eec
    local digest=e6c0385b6edb5199bb4331d48d14a7ee3afb181b0fdccfc25672f322d2ed0747
    local root=/usr/local/lib/backpack/helpers/omnitunnel
    local final="$root/$revision-$ARCH"
    if [[ -d "$final" && ! -L "$final" && -x "$final/manager" ]]; then
        info "OmniTunnel helper already installed: $final"
        return
    fi
    mkdir -p "$root"
    local stage
    stage="$(mktemp -d "$root/.stage.XXXXXX")"
    trap 'rm -rf -- "$stage"' EXIT
    fetch "https://codeload.github.com/Free-Guy-IR/OmniTunnel/tar.gz/$revision" "$stage/omni.tar.gz"
    local actual
    if command -v sha256sum >/dev/null; then
        actual="$(sha256sum "$stage/omni.tar.gz" | awk '{print $1}')"
    else
        actual="$(shasum -a 256 "$stage/omni.tar.gz" | awk '{print $1}')"
    fi
    [[ "$actual" == "$digest" ]] || { err "Checksum mismatch for OmniTunnel $revision"; exit 1; }

    local source="$stage/source" payload="$stage/payload"
    mkdir -p "$source" "$payload/bin"
    local top="OmniTunnel-$revision"
    tar -xzf "$stage/omni.tar.gz" -C "$source" --strip-components=1 \
        "$top/LICENSE" "$top/omnitunnel.sh" "$top/src" \
        "$top/bin/hysteria-$ARCH" "$top/bin/chisel-$ARCH"
    python3 "$SCRIPT_DIR/scripts/adapt-omnitunnel.py" "$source" "$payload/manager"
    bash -n "$payload/manager"
    cc -std=gnu11 -O2 -fstack-protector-strong -D_FORTIFY_SOURCE=2 -pthread -o "$payload/bin/omnitun" \
        "$source"/src/{main,obsctun,obsctcp,obscmux,obscws,icmptun,monocypher}.c
    for helper in omnistripe omniackless; do
        (cd "$source/src/$helper" && CGO_ENABLED=0 "$GO_BIN" build -trimpath -o "$payload/bin/$helper" .)
    done
    for helper in hysteria chisel; do
        install -m 0755 "$source/bin/$helper-$ARCH" "$payload/bin/$helper"
    done
    install -m 0644 "$source/LICENSE" "$payload/LICENSE"
    chmod 0755 "$payload" "$payload/bin" "$payload/manager"
    mv -Tn -- "$payload" "$final"
    [[ ! -d "$payload" ]] || { err "OmniTunnel helper directory already exists: $final"; exit 1; }
    info "Verified OmniTunnel helper installed: $final"
)
