# OmniTunnel transports

The **OmniTunnel** menu in the fork provides the [OmniTunnel engines at
acf4e6e](https://github.com/Free-Guy-IR/OmniTunnel/tree/acf4e6e5bacc4aa841320e39cff4b51e3f2a9eec).
Native GRE stays under **Setup → Direct → GRE**.

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

## Install

On both Linux amd64 or arm64 servers, update the fork checkout and enable the
helper. The installer verifies the pinned source archive, builds the routed
engines and installs the bundled Hysteria and Chisel engines.

```bash
git pull --ff-only &&
  sudo env BP_BUILD_FROM_SOURCE=1 BP_HELPERS=omni bash install.sh
```

The helper build uses a C compiler and Python 3. Running tunnels uses systemd
and the Linux networking tools. FoU needs the kernel FoU and IPv4 GRE
modules, VXLAN needs its kernel module, and the routed userspace modes need
TUN. Routed port forwards use iptables. Ackless needs nftables. Hysteria2 uses OpenSSL for setup
and Python 3 for TCP forwarding. Reverse Mux uses OpenSSL and ssh-keygen for its host identity.

## Configure

1. Open **11 → OmniTunnel → Create Iran End**. Select a mode and enter the
   IPv4 addresses assigned to the two servers. The wizard prints a setup token.
2. Open **OmniTunnel → Apply Setup Token On Kharej** on the other server and
   paste the token. Keep the token private. Hysteria2 also carries the server
   certificate and its private key inside the same token.
3. For ordinary service forwards, choose **Add TCP/UDP Forward** on Iran.
   For **Reverse Mux**, use **Add Reverse Mux Forward** and apply the
   resulting token with **Apply Reverse Mux Token** on Kharej.

The OmniTunnel menu has its own tunnel list, status and controls. Its
systemd units start with `backpack-omni-` and its configurations live in
`/etc/backpack/omnitunnel`. ICMP, FoU and VXLAN carry traffic in the clear.
Reverse Mux protects forwarded TCP streams through Chisel. Route any other
sensitive traffic through encryption of its own.

## خلاصهٔ فارسی

روی هر دو سرور فورک را با `BP_HELPERS=omni` نصب کن. از منوی اصلی گزینهٔ
**۱۱. OmniTunnel** را باز کن. اول سمت ایران را بساز و توکن را در سمت خارج
وارد کن. وضعیت و فورواردها در همان منوی OmniTunnel هستند.
