#!/usr/bin/env bash
# Run as root on a disposable Linux machine with the FoU, GRE and VXLAN modules.
set -euo pipefail

manager="${1:?installed OmniTunnel manager path}"
modprobe -a veth fou ip_gre vxlan

# Use the exact command builders written into the installed systemd units.
source <(sed -n '/^kernel_up_cmd() {$/,/^}$/p; /^kernel_down_cmd() {$/,/^}$/p' "$manager")

near="bp-omni-a-$$"
far="bp-omni-b-$$"
cleanup() {
    ip netns delete "$near" 2>/dev/null || true
    ip netns delete "$far" 2>/dev/null || true
    ip link delete "boa$$" 2>/dev/null || true
}
trap cleanup EXIT

ip netns add "$near"
ip netns add "$far"
ip link add "boa$$" type veth peer name "bob$$"
ip link set "boa$$" netns "$near"
ip link set "bob$$" netns "$far"
ip -n "$near" addr add 198.18.70.1/24 dev "boa$$"
ip -n "$far" addr add 198.18.70.2/24 dev "bob$$"
ip -n "$near" link set "boa$$" up
ip -n "$far" link set "bob$$" up

link_start() {
    local ns="$1" LOCAL_IP="$2" PEER_IP="$3" TUN_ADDR="$4" PEER_ADDR="$5"
    ip netns exec "$ns" sh -ec "$(kernel_up_cmd)"
}

link_stop() {
    local ns="$1" LOCAL_IP="$2" PEER_IP="$3"
    ip netns exec "$ns" sh -ec "$(kernel_down_cmd)"
    if ip -n "$ns" link show dev "$DEV" >/dev/null 2>&1; then
        printf '%s remained after stop\n' "$DEV" >&2
        exit 1
    fi
}

DEV=bpotest
MTU=1400
PORT=37219
for TYPE in fou vxlan; do
    link_start "$near" 198.18.70.1 198.18.70.2 10.201.7.1 10.201.7.2
    link_start "$far" 198.18.70.2 198.18.70.1 10.201.7.2 10.201.7.1
    ip netns exec "$near" ping -I 10.201.7.1 -c 2 -W 3 -s 1200 10.201.7.2
    ip netns exec "$far" ping -I 10.201.7.2 -c 2 -W 3 -s 1200 10.201.7.1
    link_stop "$near" 198.18.70.1 198.18.70.2
    link_stop "$far" 198.18.70.2 198.18.70.1
    if [[ "$TYPE" == fou && -n "$(ip netns exec "$near" ip fou show)" ]]; then
        echo "FoU socket remained after stop" >&2
        exit 1
    fi
    printf '%s passed\n' "$TYPE"
done
