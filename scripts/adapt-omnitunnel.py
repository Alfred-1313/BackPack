"""Adapt the verified OmniTunnel snapshot to BackPack's service and state paths."""

import re
import sys
from pathlib import Path


def replace_once(text, old, new):
    if text.count(old) != 1:
        raise ValueError(f"expected exactly one upstream match for {old!r}")
    return text.replace(old, new, 1)


def replace_function(text, name, replacement):
    pattern = rf"(?m)^{name}\(\) \{{\n[\s\S]*?^}}\n"
    text, count = re.subn(pattern, lambda _: replacement.strip() + "\n", text, count=1)
    if count != 1:
        raise ValueError(f"upstream function {name} was not found")
    return text


def use_private_key_environment(source):
    for name in ("obsctun", "obsctcp", "obscmux", "obscws"):
        path = source / "src" / f"{name}.c"
        text = path.read_text()
        marker = re.search(r"(?m)^    if ?\(!gL\|\|!gR\|\|!gA\|\|!gP\|\|!gk", text)
        if marker is None:
            raise ValueError(f"{name}: key validation was not found")
        text = text[:marker.start()] + (
            '    if (!gk) {\n'
            '        keyhex = getenv("BACKPACK_OMNI_KEY");\n'
            '        gk = keyhex != NULL && *keyhex != 0;\n'
            '    }\n'
        ) + text[marker.start():]
        path.write_text(text)

    for name, key in (("omnistripe", "key"), ("omniackless", "psk")):
        path = source / "src" / name / "main.go"
        body = path.read_text()
        old = "\tflag.Parse()"
        body = replace_once(body, old, old + (
            f'\n\tif *{key} == "" {{\n\t\t*{key} = os.Getenv("BACKPACK_OMNI_KEY")\n\t}}'
        ))
        body = body.replace('"/var/lib/omnitunnel/" + ', 'backpackStateDir() + "/" + ')
        body = body.replace('"/var/lib/omnitunnel/stripe-"', 'backpackStateDir() + "/stripe-"')
        body = body.replace('os.MkdirAll("/var/lib/omnitunnel",', 'os.MkdirAll(backpackStateDir(),')
        body += """
func backpackStateDir() string {
	if dir := os.Getenv("BACKPACK_OMNI_STATE_DIR"); dir != "" {
		return dir
	}
	return "/var/lib/backpack/omnitunnel"
}
"""
        if name == "omnistripe":
            body = body.replace("runServer(base, count, conc int,", "runServer(local string, base, count, conc int,")
            body = replace_once(body, 'net.JoinHostPort("0.0.0.0", strconv.Itoa(p))', 'net.JoinHostPort(local, strconv.Itoa(p))')
            body = replace_once(body, "tn.runServer(*basePort,", "tn.runServer(*localIP, *basePort,")
            body = replace_once(body, 'fmt.Fprintf(os.Stderr, "omnistripe: listen %s: %v\\n", addr, err)\n\t\t\tcontinue',
                                'fmt.Fprintf(os.Stderr, "omnistripe: listen %s: %v\\n", addr, err)\n\t\t\tos.Exit(1)')
        path.write_text(body)


def manager(source):
    text = source.read_text()
    text = replace_once(text, "\nset -euo pipefail\n", "\nset -euo pipefail\numask 077\n")
    for old, new in (
        ('ROOT_DIR="/etc/omnitunnel"', 'ROOT_DIR="/etc/backpack/omnitunnel"'),
        ('VARLIB_DIR="/var/lib/omnitunnel"', 'VARLIB_DIR="/var/lib/backpack/omnitunnel"'),
        ('RUN_DIR="/run/omnitunnel"', 'RUN_DIR="/run/backpack/omnitunnel"'),
        ('ASSET_DIR="${OMNITUN_ASSETS:-/opt/omnitunnel}"', 'ASSET_DIR="$VARLIB_DIR"'),
        ('FWD_SYSCTL_FILE="/etc/sysctl.d/99-omnitunnel-forwarding.conf"',
         'FWD_SYSCTL_FILE="/etc/sysctl.d/99-backpack-omnitunnel-forwarding.conf"'),
    ):
        text = replace_once(text, old, new)
    text = text.replace("omnitun-", "backpack-omni-").replace("TSUITE_", "BPOMNI_")
    text = text.replace('local dev="ts-$n"', 'local dev="bpo-$n"')
    text = text.replace('&& dev="ts$(printf', '&& dev="bpo$(printf')
    for var, tool in (
        ("BPOMNI_BIN", "omnitun"),
        ("HYSTERIA_BIN", "hysteria"),
        ("CHISEL_BIN", "chisel"),
        ("OMNISTRIPE_BIN", "omnistripe"),
        ("OMNIACKLESS_BIN", "omniackless"),
    ):
        text, count = re.subn(
            rf"(?m)^{var}=.*$", lambda _: f'{var}="$SCRIPT_DIR/bin/{tool}"', text
        )
        if count != 1:
            raise ValueError(f"upstream path {var} was not found")
        check = "ensure_binary" if tool == "omnitun" else f"ensure_{tool}"
        text = replace_function(text, check, f'''{check}() {{
    [[ -x "${var}" ]] || die "the verified {tool} helper is missing; reinstall BackPack with BP_HELPERS=omni"
}}''')

    text = text.replace(" -k $KEY", "").replace(" -to 1500 -F ", " -to 1500 ")
    text = replace_once(text, '    else\n        cat > "$unit" <<EOF\n', '''    else
        printf '%s\\n' "BACKPACK_OMNI_KEY=$KEY" "BACKPACK_OMNI_STATE_DIR=$VARLIB_DIR" > "$(inst_path "$1")/engine.env"
        cat > "$unit" <<EOF
''')
    text = replace_once(text, "ExecStart=$(build_execstart)", (
        'EnvironmentFile=$(inst_path "$1")/engine.env\nExecStart=$(build_execstart)'
    ))
    text = replace_function(text, "apply_tuning", """
apply_tuning() {
    # Routed forwards need forwarding. Raw IP links themselves do not.
    [[ -n "$(pf_entries "$(inst_pf "$1")")" ]] && ensure_forwarding "$DEV"
    return 0
}
""")
    text = replace_once(text, 'apply_tuning "$DEV" "$SHAPE";', 'apply_tuning "$1";')
    text = text.replace("ExecStartPre=-$SCRIPT_PATH _prefwd\n", "")
    text = text.replace("ExecStartPre=-/sbin/ip link del $DEV\n", "")
    text = replace_once(text, '    local npf; npf="$(inst_pf "$n")"',
                        '    if ip link show dev "$dev" >/dev/null 2>&1; then die "interface $dev is already in use"; fi\n'
                        '    local npf; npf="$(inst_pf "$n")"')
    text = text.replace("Wants=$(svc_name", "BindsTo=$(svc_name")
    return text


def kernel_links(text):
    text = replace_function(text, "kernel_up_cmd", r'''
kernel_up_cmd() {
    case "$TYPE" in
        gre) echo "ip link add $DEV type gre local $LOCAL_IP remote $PEER_IP ttl 255 key $PORT && { ip addr add $TUN_ADDR peer $PEER_ADDR dev $DEV && ip link set $DEV up mtu $MTU || { ip link del $DEV; exit 1; }; }";;
        fou)
            echo "ip fou add port $PORT ipproto 47 local $LOCAL_IP peer $PEER_IP peer_port $PORT && { ip link add $DEV type gre local $LOCAL_IP remote $PEER_IP ttl 255 key $PORT encap fou encap-sport $PORT encap-dport $PORT || { ip fou del port $PORT local $LOCAL_IP peer $PEER_IP peer_port $PORT; exit 1; }; } && { ip addr add $TUN_ADDR peer $PEER_ADDR dev $DEV && ip link set $DEV up mtu $MTU || { ip link del $DEV; ip fou del port $PORT local $LOCAL_IP peer $PEER_IP peer_port $PORT; exit 1; }; }";;
        vxlan)
            echo "ip link add $DEV type vxlan id $PORT local $LOCAL_IP remote $PEER_IP dstport $PORT ttl 255 && { ip addr add $TUN_ADDR/30 dev $DEV && ip link set $DEV up mtu $MTU || { ip link del $DEV; exit 1; }; }";;
    esac
}
''')
    text = replace_once(text, 'kernel_down_cmd() { echo "ip link del $DEV 2>/dev/null"; }', r'''
kernel_down_cmd() {
    if [[ "$TYPE" == fou ]]; then
        echo "ip link del $DEV; ip fou del port $PORT local $LOCAL_IP peer $PEER_IP peer_port $PORT"
    else
        echo "ip link del $DEV"
    fi
}'''.strip())
    # Stopping the owning service closes a TUN or removes the kernel link via
    # ExecStop. A failed creation must not remove another unit's interface.
    return replace_once(text, '_pf_flush_chain "$n"; ip link del "$dev" 2>/dev/null || true', '_pf_flush_chain "$n"')


def hysteria_tls(text):
    text = replace_function(text, "hy_cert", """
hy_cert() {
    [[ -s "$1/hy.crt" && -s "$1/hy.key" ]] || die "missing Hysteria server certificate; re-apply this tunnel's setup token"
}
""")
    text = replace_once(text, """masquerade:
  type: proxy
  proxy:
    url: https://$HY_MASQ_HOST/
    rewriteHost: true
""", "")
    text = replace_once(text, """tls:
  insecure: true
  sni: $HY_MASQ_HOST
""", """tls:
  ca: $d/hy.crt
  sni: $PEER_IP
lazy: true
""")
    text = replace_once(text, '    inst_enable "$kn"\n    # Server-side params', r'''
    local hy_material=""
    if type_is_hysteria "$t"; then
        local d; d="$(inst_path "$kn")"
        if ! openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 365 \
            -subj "/CN=$fhost" -addext "subjectAltName=IP:$fhost" \
            -keyout "$d/hy.key" -out "$d/hy.crt" >/dev/null 2>&1; then
            die "could not generate the Hysteria TLS identity for $fhost"
        fi
        hy_material="$(base64 -w 0 "$d/hy.crt")|$(base64 -w 0 "$d/hy.key")"
        rm -f "$d/hy.key"
    fi
    inst_enable "$kn"
    inst_running "$kn" || die "local side '$kn' failed to start; check journalctl -u $(svc_name "$kn")"
    # Server-side params'''.lstrip("\n"))
    text = replace_once(text, '    echo "  ${C_CYAN}bash <(curl -fsSL $RAW_BASE/install.sh)${C_RESET}"\n    echo "  ${C_CYAN}omnitunnel server-token $token${C_RESET}"', r'''
    if [[ -n "$hy_material" ]]; then
        token="$( { printf '%s' "$token" | base64 -d; printf '|1|%s' "$hy_material"; } | base64 -w 0 )"
    fi
    echo "  sudo backpack omni join"
    echo "  $token"'''.lstrip("\n"))
    text = replace_once(text, 'local name t fhost mylip port key pta ppa shape nc pmode lanes\n    IFS=', 'local name t fhost mylip port key pta ppa shape nc pmode lanes cert keypem\n    IFS=')
    text = replace_once(text, 'pmode lanes <<< "$dec"\n    inst_name_ok', 'pmode lanes cert keypem <<< "$dec"\n    inst_name_ok')
    text = replace_once(text, '    create_inst "$name" "$t" server "$fhost" "$mylip" "$port" "$key" "$pta" "$ppa" "$shape" "$nc" "$pmode" "" "$lanes"\n    inst_enable "$name"', r'''
    if type_is_hysteria "$t"; then
        local cert_body key_body cert_key server_key
        cert_body="$(printf '%s' "$cert" | base64 -d)" || die "invalid TLS certificate in the setup token"
        key_body="$(printf '%s' "$keypem" | base64 -d)" || die "invalid TLS key in the setup token"
        cert_key="$(printf '%s\n' "$cert_body" | openssl x509 -pubkey -noout)" || die "invalid TLS certificate"
        server_key="$(printf '%s\n' "$key_body" | openssl pkey -pubout)" || die "invalid TLS key"
        [[ "$cert_key" == "$server_key" ]] || die "certificate and key do not match"
        printf '%s\n' "$cert_body" | openssl x509 -checkip "$fhost" -noout >/dev/null || die "TLS certificate does not name $fhost"
    fi
    create_inst "$name" "$t" server "$fhost" "$mylip" "$port" "$key" "$pta" "$ppa" "$shape" "$nc" "$pmode" "" "$lanes"
    if type_is_hysteria "$t"; then
        printf '%s\n' "$cert_body" > "$(inst_path "$name")/hy.crt"
        printf '%s\n' "$key_body" > "$(inst_path "$name")/hy.key"
    fi
    inst_enable "$name"'''.lstrip("\n"))
    text = replace_once(text, '    tok_host_ok "$fhost" || die "invalid token: foreign address"\n    tok_host_ok "$mylip" || die "invalid token: near address"\n    tok_port_ok "$port"',
                        '    tok_ipv4_ok "$fhost" || die "invalid token: foreign address"\n'
                        '    tok_ipv4_ok "$mylip" || die "invalid token: near address"\n'
                        '    [[ "$fhost" != "$mylip" ]] || die "the tunnel endpoints must be different"\n    tok_port_ok "$port"')
    text = replace_once(text, """        warn "instance '$name' already exists - replacing it"; inst_remove "$name" >/dev/null 2>&1 || true""", r"""        [[ "$TYPE" == "$t" && "$LOCAL_IP" == "$fhost" && "$PEER_IP" == "$mylip" &&
            "$PORT" == "$port" && "$KEY" == "$key" && "$TUN_ADDR" == "$pta" && "$PEER_ADDR" == "$ppa" ]] ||
            die "a different tunnel already uses the name '$name'"
        if inst_running "$name"; then ok "foreign tunnel '$name' is already running"; return; fi
        inst_remove "$name" >/dev/null 2>&1 || true""")
    return text


def reverse_mux(text):
    text = replace_once(text, '[[ "$1" != icmp && "$1" != gre && "$1" != fou && "$1" != vxlan && "$1" != reverse-mux ]]',
                        '[[ "$1" != icmp && "$1" != gre && "$1" != fou && "$1" != vxlan ]]')
    text = replace_once(text, r"""    { printf 'MUX1|%s|%s|%s\n' "$1" "$TUN_ADDR" "$ctrl"; cat "$(mux_conf "$1")" 2>/dev/null; } | base64 | tr -d '\n'""",
                        '''    local finger
    finger="$(ssh-keygen -y -f "$(inst_path "$1")/mux.key" | ssh-keygen -lf - | awk '{ sub(/^SHA256:/, "", $2); print $2 "=" }')"
    [[ "$finger" =~ ^[A-Za-z0-9+/]{43}=$ ]] || die "could not derive the Reverse Mux host fingerprint"
    { printf 'MUX2|%s|%s|%s|%s\\n' "$1" "$TUN_ADDR" "$ctrl" "$finger"; cat "$(mux_conf "$1")"; } | base64 -w 0''')
    text = replace_once(text, '    local ctrl; ctrl="$(mux_ctrl_port "$1")"\n    local new=', r'''
    local ctrl; ctrl="$(mux_ctrl_port "$1")"
    local dir; dir="$(inst_path "$1")"
    if [[ ! -s "$dir/mux.key" ]]; then
        openssl ecparam -name prime256v1 -genkey -noout -out "$dir/mux.key" || die "could not create Reverse Mux host key"
    fi
    tok_hexkey_ok "$KEY" || die "Reverse Mux needs the shared key from its BackPack setup token"
    printf '{"backpack:%s":["^R:0[.]0[.]0[.]0:[0-9]+$"]}\n' "$KEY" > "$dir/mux-auth.json"
    local new='''.lstrip("\n"))
    text = replace_once(text, 'ExecStart=$CHISEL_BIN server --reverse --host $TUN_ADDR --port $ctrl',
                        'ExecStart=$CHISEL_BIN server --reverse --host $TUN_ADDR --port $ctrl --keyfile $dir/mux.key --authfile $dir/mux-auth.json')
    text = replace_once(text, '''    local inst="$1" relay_ip="$2" ctrl="$3"; shift 3''',
                        '''    local inst="$1" relay_ip="$2" ctrl="$3" finger="$4"; shift 4
    load_inst "$inst"
    local dir; dir="$(inst_path "$inst")"
    tok_hexkey_ok "$KEY" || die "Reverse Mux needs the shared key from its BackPack setup token"
    printf '%s\\n' "AUTH=backpack:$KEY" > "$dir/mux.env"''')
    text = replace_once(text, 'ExecStart=$CHISEL_BIN client --keepalive 25s', 'EnvironmentFile=$dir/mux.env\nExecStart=$CHISEL_BIN client --fingerprint $finger --keepalive 25s')
    text = replace_function(text, "_mux_push_foreign", r'''
_mux_push_foreign() {
    local tok; tok="$(mux_token "$1")"
    echo "On the foreign server run: sudo backpack omni mux-join"
    echo "  $tok"
}
''')
    text = replace_once(text, '''    case "$head" in MUX1\\|*) :;; *) die "invalid mux token";; esac
    local _tag inst relay_ip ctrl; IFS='|' read -r _tag inst relay_ip ctrl <<< "$head"''',
                        r'''    case "$head" in MUX2\|*) :;; *) die "invalid BackPack Reverse Mux token";; esac
    local _tag inst relay_ip ctrl finger; IFS='|' read -r _tag inst relay_ip ctrl finger <<< "$head"
    [[ "$finger" =~ ^[A-Za-z0-9+/]{43}=$ ]] || die "invalid token: host fingerprint"''')
    text = replace_once(text, '''    local maps=() ln
    while IFS= read -r ln; do case "$ln" in MUX1\\|*|'')''',
                        r'''    inst_exists "$inst" || die "apply the tunnel setup token first"
    load_inst "$inst"
    [[ "$ROLE" == server && "$TYPE" == reverse-mux && "$relay_ip" == "$PEER_ADDR" ]] ||
        die "this Reverse Mux token does not match the local tunnel"
    local maps=() ln
    while IFS= read -r ln; do case "$ln" in MUX2\|*|'')''')
    text = replace_once(text, 'mux_client_apply "$inst" "$relay_ip" "$ctrl" "${maps[@]}"',
                        'mux_client_apply "$inst" "$relay_ip" "$ctrl" "$finger" "${maps[@]}"')
    text = replace_once(text, '''    [[ "$ROLE" == server ]] && die "run mux-add on the RELAY (client) side of '$inst', not the foreign"''',
                        '''    [[ "$ROLE" == client && "$TYPE" == reverse-mux ]] || die "mux-forward requires the Iran side of a Reverse Mux tunnel"''')
    return text


def commands(text):

    text = replace_function(text, "cmd_mux_del", r"""
cmd_mux_del() {
    local inst="$1" pub
    pub="$(norm_port "$2")" || die "public port must be 1-65535"
    load_inst "$inst"
    [[ "$ROLE" == client && "$TYPE" == reverse-mux ]] || die "mux-remove requires the Iran side of a Reverse Mux tunnel"
    local mc; mc="$(mux_conf "$inst")"
    rewrite_without "$mc" "^0*$pub:" || die "could not rewrite $mc"
    mux_relay_reconcile "$inst"
    if [[ -s "$mc" ]]; then
        _mux_push_foreign "$inst" "$PEER_IP"
    else
        printf 'On Kharej run: sudo backpack omni mux-off %s\n' "$inst"
    fi
}
""")
    text = replace_once(text, '''    echo "${C_GREEN}${C_BOLD}Near (Iran) side '$kn' is up.${C_RESET}"''',
                        "    echo \"Local OmniTunnel service started\"")
    start = text.index('case "${1:-menu}" in\n')
    text = text[:start] + r'''
need_root
case "${1:-}" in
    add-manual)
        case "$2" in
            ackless) command -v nft >/dev/null || die "Ackless requires nftables (nft)";;
            hysteria) command -v openssl >/dev/null || die "Hysteria2 setup requires OpenSSL";;
        esac
        shift; cmd_add_manual "$@";;
    server-token) IFS= read -r token; cmd_server_token "$token";;
    list) for n in $(list_instances); do inst_status "$n"; done;;
    status) validate_inst_name "$2"; inst_status "$2"; pf_status_report "$2";;
    enable)
        validate_inst_name "$2"; inst_enable "$2"; inst_running "$2"
        if [[ -f "/etc/systemd/system/$(mux_svc "$2")" ]]; then systemctl start "$(mux_svc "$2")"; fi;;
    stop) validate_inst_name "$2"; inst_exists "$2" || die "unknown tunnel: $2"; systemctl stop "$(svc_name "$2")";;
    remove) validate_inst_name "$2"; inst_remove "$2";;
    pf-add)
        validate_inst_name "$2"; load_inst "$2"
        if ! type_is_hysteria "$TYPE"; then ensure_forwarding "$DEV"; fi
        pf_add "$2" "$3" "$4";;
    pf-del) validate_inst_name "$2"; shift; cmd_pf_del "$@";;
    pf-check) validate_inst_name "$2"; pf_check "$2";;
    mux-add)
        validate_inst_name "$2"
        command -v openssl >/dev/null && command -v ssh-keygen >/dev/null || die "Reverse Mux setup requires OpenSSL and ssh-keygen"
        shift; cmd_mux_add "$@";;
    mux-del) validate_inst_name "$2"; shift; cmd_mux_del "$@";;
    mux-token) IFS= read -r token; cmd_mux_token "$token";;
    mux-off) validate_inst_name "$2"; cmd_mux_off "$2";;
    _postup) validate_inst_name "$2"; cmd_postup "$2";;
    *) die "use the BackPack OmniTunnel menu or backpack omni help";;
esac
'''
    return text


if __name__ == "__main__":
    root, output = map(Path, sys.argv[1:])
    use_private_key_environment(root)
    body = manager(root / "omnitunnel.sh")
    body = commands(reverse_mux(hysteria_tls(kernel_links(body))))
    output.write_text(body)
