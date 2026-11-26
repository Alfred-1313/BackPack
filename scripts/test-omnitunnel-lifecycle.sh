#!/usr/bin/env bash
# Exercise the installed manager with disposable config and simulated systemd.
set -euo pipefail

manager="${1:?installed OmniTunnel manager path}"
scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/units" "$scratch/commands"
sed -e "s#/etc/backpack/omnitunnel#$scratch/config#g" \
    -e "s#/var/lib/backpack/omnitunnel#$scratch/state#g" \
    -e "s#/run/backpack/omnitunnel#$scratch/run#g" \
    -e "s#/etc/systemd/system#$scratch/units#g" "$manager" > "$scratch/manager"
ln -s "$(dirname "$manager")/bin" "$scratch/bin"
export OMNI_TEST_UNITS="$scratch/units"
cat > "$scratch/commands/systemctl" <<'EOF'
#!/usr/bin/env bash
set -eu
cd "$OMNI_TEST_UNITS"
case "$1" in
    restart)
        [[ -f "$2" ]]
        printf '%s\n' "$2" >> restarted
        touch "$2.active";;
    stop) rm -f -- backpack-omni-demo*.active;;
    is-active) [[ -e "${*: -1}.active" ]];;
    daemon-reload|enable) ;;
    list-units) :;;
esac
EOF
# Avoid touching any actual link or firewall while running the manager.
printf '#!/bin/sh\nexit 1\n' > "$scratch/commands/ip"
cp "$scratch/commands/ip" "$scratch/commands/iptables"
chmod +x "$scratch/commands/"*
export PATH="$scratch/commands:$PATH"

join() { printf '%s\n' "$token" | bash "$scratch/manager" server-token; }
token_data="demo|tcp|198.18.0.2|198.18.0.1|27000|$(printf '01%.0s' {1..32})|10.201.9.2|10.201.9.1|none|16|"
token="$(printf '%s' "$token_data" | base64 -w 0)"
join
instance="$scratch/config/inst/demo"
cp "$instance/instance.conf" "$scratch/original.conf"
printf 'tcp:8000:8080\n' > "$instance/pf.conf"
touch "$scratch/units/backpack-omni-demo-pf-8000.service" "$scratch/units/backpack-omni-demo-mux.service"
bash "$scratch/manager" stop demo
join
cmp "$instance/instance.conf" "$scratch/original.conf"
grep -qxF 'tcp:8000:8080' "$instance/pf.conf"
test -f "$scratch/units/backpack-omni-demo-pf-8000.service.active"
test -f "$scratch/units/backpack-omni-demo-mux.service.active"
cp "$scratch/units/restarted" "$scratch/restarted"
join
cmp "$scratch/units/restarted" "$scratch/restarted"

token="$(printf '%s' "${token_data/27000/27001}" | base64 -w 0)"
if join; then
    echo 'A different setup token was accepted' >&2
    exit 1
fi
cmp "$instance/instance.conf" "$scratch/original.conf"
# A test's removal must leave neither the network configuration nor its lock.
test_name=bpct0123456789ab
test_token="${token_data/demo/$test_name}"
test_token="${test_token/27000/27002}"
test_token="${test_token//10.201.9./10.201.10.}"
test_encoded="$(printf '%s' "$test_token" | base64 -w 0)"
printf '%s\n' "$test_encoded" | bash "$scratch/manager" server-token
test_inst="$scratch/config/inst/$test_name"
test_lock="$scratch/run/pf.BPOMNI_BPCT0123456789AB.lock"
other_lock="$scratch/run/pf.BPOMNI_DEMO.lock"
printf 'tcp:8000:8080\n' > "$test_inst/pf.conf"
touch "$test_lock" "$other_lock"
bash "$scratch/manager" remove "$test_name"
test ! -e "$test_inst"
test ! -e "$test_lock"
test ! -d "$scratch/config/removed"
touch "$test_lock"
bash "$scratch/manager" remove "$test_name"
test ! -e "$test_lock"
test -f "$other_lock"

printf 'KEY=$(touch %q)\n' "$scratch/executed" >> "$instance/instance.conf"
if bash "$scratch/manager" status demo; then
    echo 'Invalid config was accepted' >&2
    exit 1
fi
test ! -e "$scratch/executed"
