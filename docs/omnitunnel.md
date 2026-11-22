# OmniTunnel transports

The **OmniTunnel** menu in the fork provides the [OmniTunnel engines at
acf4e6e](https://github.com/Free-Guy-IR/OmniTunnel/tree/acf4e6e5bacc4aa841320e39cff4b51e3f2a9eec).

| OmniTunnel mode | What it carries |
| --- | --- |
| ICMP | Plain IP packets over ICMP. Use native xDi for BackPack's encrypted ICMP mode. |
| Routed UDP, TCP, Mux and WS | Complete IP packets through an encrypted UDP, TCP or WebSocket carrier. |
| Hysteria2 | TCP and UDP port forwards over QUIC, using certificate verification. |
| FoU | GRE encapsulated in UDP. |
| VXLAN | Ethernet encapsulated in UDP. |
| Reverse Mux | A single authenticated multiplexed connection for multiple TCP forwards over the ICMP link. |
| Stripe | Routed IP using multiple TCP connections. |
| Ackless | Routed IP using authenticated raw TCP packets. |

Choose BackPack’s **GRE** under **Direct** for plain kernel GRE over IPv4
or IPv6. Existing helper GRE tunnels and their setup tokens are still supported.

## Install

On both Linux amd64 or arm64 servers, update the fork checkout and enable the
helper. The installer verifies the pinned source archive, builds the routed
engines and installs the bundled Hysteria and Chisel engines.

```bash
git pull --ff-only &&
  sudo env BP_BUILD_FROM_SOURCE=1 BP_HELPERS=omni bash install.sh
```

The helper build uses a C compiler and Python 3. Running tunnels uses systemd
and the Linux networking tools. FoU needs the FoU and IPv4 GRE modules and a
kernel built with `CONFIG_NET_FOU_IP_TUNNELS`. VXLAN needs its kernel module
and the routed userspace modes need TUN. Routed port forwards use iptables. Ackless needs nftables. Hysteria2 uses OpenSSL for setup
and Python 3 for TCP forwarding. Reverse Mux uses OpenSSL and ssh-keygen for its host identity.

## Configure

1. Choose the carrier under **Setup Iran ? Direct** in the CLI or
   **Add a tunnel ? Direct ? Tunnel** in the panel. Enter the IPv4 addresses
   assigned to the two servers. You can specify the listener port on Kharej.
   Select Reverse for Reverse Mux. Stripe needs a free block of 400 consecutive TCP ports starting there.
2. On Kharej, choose the same transport and apply the setup token. The CLI
   also accepts it through `sudo backpack omni join`. Keep the token private.
   Hysteria2 carries the server certificate and key in its setup token.
3. For ordinary service forwards, choose **Add TCP/UDP Forward** on Iran.
   For **Reverse Mux**, use **Add Reverse Mux Forward** and apply the
   resulting token with **Apply Reverse Mux Token** on Kharej.

The OmniTunnel CLI menu and **Manage → Routed tunnels** in the panel list
and manage existing helper tunnels. Their systemd units start with `backpack-omni-` and its configurations live in
`/etc/backpack/omnitunnel`. ICMP, FoU and VXLAN carry traffic in the clear.
Reverse Mux protects forwarded TCP streams through Chisel. Route any other
sensitive traffic through encryption of its own.

Use **Start Or Restart** to start the tunnel and its configured forwarding
services. You can also paste the same setup token again on Kharej to resume an
existing tunnel with its forwards. The CLI requires `--force` to remove the
last forward. The menu asks you to confirm the removal.

## Connection Test

The standard Connection Test includes the helper carriers and GOST
Direct when both servers use a current build. Its native GRE row tests plain kernel GRE.
On the IPv4 path it creates a temporary pair per carrier, interleaves its run
with the native speed tests, checks the same stability echoes and speed samples
as the other transports, then removes the test pair. An unavailable
helper or a setup failure is reported as skipped with its setup diagnostic.
The probe sends traffic on the selected route; support for a carrier is
independent of how it performs on that route.

Use **Test a configured OmniTunnel or GOST connection** in Connection Test,
or **Connection Test ? Configured tunnel** in the CLI. Start the temporary
listener on an unused target port on Kharej. Paste its private test code on
Iran and probe the forwarded port, or use the Kharej tunnel IP and the same
port on both forms for a routed carrier. A successful result verifies that
the specific Kharej listener returned its one-time payload through the tunnel.
The listener expires after five minutes. Use a different test target when a
production application already owns the port.

<div dir="rtl">

## خلاصهٔ فارسی

روی هر دو سرور فورک را با `BP_HELPERS=omni` نصب کن. از منوی اصلی گزینهٔ
**۱۱. OmniTunnel** را باز کن. اول سمت ایران را بساز و توکن را در سمت خارج
وارد کن. وضعیت و فورواردها در همان منوی OmniTunnel هستند.

</div>

*Last verified against Backpack v2.0.0.*
