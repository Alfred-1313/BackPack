#!/usr/bin/env bash
# Loaded by install.sh after source-build dependencies are ready.
install_omnitunnel() (
    set -euo pipefail
    local revision=acf4e6e5bacc4aa841320e39cff4b51e3f2a9eec
    local digest=e6c0385b6edb5199bb4331d48d14a7ee3afb181b0fdccfc25672f322d2ed0747
    local root=/usr/local/lib/backpack/helpers/omnitunnel
    local final="$root/$revision-$ARCH"
    local adapter="$SCRIPT_DIR/scripts/adapt-omnitunnel.py"
    local -a checksum helpers=(omnitun omnistripe omniackless hysteria chisel)
    local relay="$SCRIPT_DIR/scripts/omni-socks-forward.py"
    local helper adapter_digest complete=1
    if command -v sha256sum >/dev/null; then checksum=(sha256sum); else checksum=(shasum -a 256); fi
    adapter_digest="$("${checksum[@]}" "$adapter" | awk '{print $1}')"
    mkdir -p "$root"
    local lock
    exec {lock}>"$root/.install.lock"
    flock "$lock"
    if [[ -L "$final" || (-e "$final" && ! -d "$final") || -L "$final/bin" ]]; then
        err "Invalid OmniTunnel helper directory: $final"
        exit 1
    fi
    for helper in "${helpers[@]}"; do
        [[ -f "$final/bin/$helper" && ! -L "$final/bin/$helper" && -x "$final/bin/$helper" ]] || complete=0
    done
    cmp -s "$relay" "$final/bin/socksfwd.py" || complete=0
    if [[ "$complete" == 1 && -f "$final/manager" && ! -L "$final/manager" && -x "$final/manager" ]] &&
        [[ "$(cat "$final/.adapter-sha256" 2>/dev/null)" == "$adapter_digest" ]]; then
        info "OmniTunnel helper already installed: $final"
        return
    fi
    local stage
    stage="$(mktemp -d "$root/.stage.XXXXXX")"
    trap 'rm -rf -- "$stage"' EXIT
    fetch "https://codeload.github.com/Free-Guy-IR/OmniTunnel/tar.gz/$revision" "$stage/omni.tar.gz"
    local actual
    actual="$("${checksum[@]}" "$stage/omni.tar.gz" | awk '{print $1}')"
    [[ "$actual" == "$digest" ]] || { err "Checksum mismatch for OmniTunnel $revision"; exit 1; }

    local source="$stage/source" payload="$stage/payload"
    mkdir -p "$source" "$payload/bin"
    local top="OmniTunnel-$revision"
    tar -xzf "$stage/omni.tar.gz" -C "$source" --strip-components=1 \
        "$top/LICENSE" "$top/omnitunnel.sh" "$top/src" \
        "$top/bin/hysteria-$ARCH" "$top/bin/chisel-$ARCH"
    python3 "$adapter" "$source" "$payload/manager"
    bash -n "$payload/manager"
    cc -std=gnu11 -O2 -fstack-protector-strong -D_FORTIFY_SOURCE=2 -pthread -o "$payload/bin/omnitun" \
        "$source"/src/{main,obsctun,obsctcp,obscmux,obscws,icmptun,monocypher}.c
    for helper in omnistripe omniackless; do
        (cd "$source/src/$helper" && CGO_ENABLED=0 "$GO_BIN" build -trimpath -o "$payload/bin/$helper" .)
    done
    for helper in hysteria chisel; do
        install -m 0755 "$source/bin/$helper-$ARCH" "$payload/bin/$helper"
    done
    install -m 0755 "$relay" "$payload/bin/socksfwd.py"
    install -m 0644 "$source/LICENSE" "$payload/LICENSE"
    chmod 0755 "$payload" "$payload/bin" "$payload/manager"
    printf '%s\n' "$adapter_digest" > "$payload/.adapter-sha256"
    if [[ -d "$final" ]]; then
        # Existing units reference these paths. Replace verified files by rename
        # so running processes keep their binaries and new starts see the update.
        mkdir -p -m 0755 "$final/bin"
        rm -f -- "$final/.adapter-sha256"
        for helper in "${helpers[@]}"; do
            mv -fT -- "$payload/bin/$helper" "$final/bin/$helper"
        done
        mv -fT -- "$payload/bin/socksfwd.py" "$final/bin/socksfwd.py"
        mv -fT -- "$payload/LICENSE" "$final/LICENSE"
        mv -fT -- "$payload/manager" "$final/manager"
        mv -fT -- "$payload/.adapter-sha256" "$final/.adapter-sha256"
    else
        mv -T -- "$payload" "$final"
    fi
    info "Verified OmniTunnel helper installed: $final"
)
