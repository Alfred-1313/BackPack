#!/usr/bin/env bash
# Render both ends of each mode. All state is temporary; no services are run.
set -euo pipefail
manager="${1:?path to the adapted manager}"
relay="$(cd "$(dirname "$0")" && pwd)/omni-socks-forward.py"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/bin" "$work/units"
for tool in omnitun omnistripe omniackless hysteria chisel; do
    printf '#!/bin/sh\nexit 80\n' > "$work/bin/$tool"
    chmod +x "$work/bin/$tool"
done
cp "$relay" "$work/bin/socksfwd.py"

# Load only the function definitions. Redirect every output path before
# calling the actual instance writer and systemd / YAML renderers.
sed -e '/^need_root$/,$d' \
    -e "s#/etc/backpack/omnitunnel#$work/config#g" \
    -e "s#/var/lib/backpack/omnitunnel#$work/state#g" \
    -e "s#/run/backpack/omnitunnel#$work/run#g" \
    -e "s#/etc/systemd/system#$work/units#g" "$manager" > "$work/manager"
source "$work/manager"
systemctl() { return 0; }
ip() { return 1; }  # Fresh fixture, no existing links.
key="$(printf 'ab%.0s' {1..32})"
modes=(icmp udp tcp mux ws hysteria fou vxlan reverse-mux stripe ackless)
for mode in "${modes[@]}"; do
    for role in client server; do
        name="$mode-$role"
        ta=10.201.7.1 pa=10.201.7.2 lip=198.18.0.1 pip=198.18.0.2
        if [[ "$role" == server ]]; then
            ta=10.201.7.2 pa=10.201.7.1 lip=198.18.0.2 pip=198.18.0.1
        fi
        instance_key=""
        if type_uses_key "$mode"; then instance_key="$key"; fi
        create_inst "$name" "$mode" "$role" "$lip" "$pip" 27001 "$instance_key" "$ta" "$pa"
        dir="$(inst_path "$name")"
        if [[ "$mode" == hysteria ]]; then
            # Rendering checks the files exist. No handshake uses these fixtures.
            printf 'fixture\n' > "$dir/hy.crt"
            printf 'fixture\n' > "$dir/hy.key"
            printf 'tcp:8111:8112\nudp:8221:8222\n' > "$dir/pf.conf"
        fi
        write_service "$name"
        unit="$work/units/$(svc_name "$name")"
        case "$mode" in
            hysteria)
                grep -Fqx "ExecStart=$HYSTERIA_BIN $role -c $dir/hy.yaml" "$unit"
                if [[ "$role" == client ]]; then
                    grep -Fqx "server: $pip:27001" "$dir/hy.yaml"
                    grep -Fq 'remote: 127.0.0.1:8222' "$dir/hy.yaml"
                    hy_reconcile_relays "$name"
                    cmp "$relay" "$HY_RELAY"
                    grep -Fq "8111 127.0.0.1 27001 127.0.0.1 8112" "$work/units/$(hy_relay_unit "$name" 8111)"
                else
                    grep -Fqx 'listen: :27001' "$dir/hy.yaml"
                fi ;;
            vxlan|fou)
                grep -Fqx 'Type=oneshot' "$unit"
                grep -Fq "local $lip" "$unit"
                grep -Fq "remote $pip" "$unit"
                grep -Fq "ExecStop=" "$unit" ;;
            *)
                case "$mode" in
                    reverse-mux) engine="$BPOMNI_BIN icmp" ;;
                    stripe) engine="$OMNISTRIPE_BIN" ;;
                    ackless) engine="$OMNIACKLESS_BIN" ;;
                    *) engine="$BPOMNI_BIN $mode" ;;
                esac
                grep -Fq "ExecStart=$engine -L $lip -R $pip -A $ta -P $pa" "$unit"
                grep -Fqx "EnvironmentFile=$dir/engine.env" "$unit" ;;
        esac
        if grep -Fq "$key" "$unit"; then echo "secret in $name unit" >&2; exit 1; fi
        printf '%s %s OK\n' "$mode" "$role"
    done
done
