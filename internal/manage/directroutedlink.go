package manage

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/easytier"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

func (n *NewDirectTunnel) setL2TPIDs() error {
	if n.L2TPTunnelID == 0 && n.L2TPPeerTunnelID == 0 && n.L2TPSessionID == 0 && n.L2TPPeerSessionID == 0 {
		n.L2TPTunnelID = 1000 + rand.Uint32N(1<<30)
		n.L2TPPeerTunnelID = 1000 + rand.Uint32N(1<<30)
		n.L2TPSessionID = n.L2TPTunnelID
		n.L2TPPeerSessionID = n.L2TPPeerTunnelID
	}
	if n.L2TPTunnelID == 0 || n.L2TPPeerTunnelID == 0 || n.L2TPSessionID == 0 || n.L2TPPeerSessionID == 0 {
		return fmt.Errorf("fill all four L2TPv3 IDs, or leave all four blank to choose them")
	}
	for _, c := range existingL3Configs() {
		if c.Carrier == l3.CarrierL2TPv3 && c.L2TPTunnelID == n.L2TPTunnelID {
			return fmt.Errorf("L2TPv3 tunnel ID %d is already used on this server", n.L2TPTunnelID)
		}
	}
	return nil
}

func (n NewDirectTunnel) easyTierSpec(side directSide) (l3Spec, error) {
	port := strings.TrimSpace(n.TunnelPort)
	if !validPort(port) {
		return l3Spec{}, fmt.Errorf("choose an EasyTier port between 1 and 65535")
	}
	name := strings.TrimSpace(n.Name)
	if name == "" {
		name = freeName("easytier-" + side.String())
	}
	if !validName(name) {
		return l3Spec{}, fmt.Errorf("invalid tunnel name %q", name)
	}
	if fileExists(app.ConfigPath(name)) {
		return l3Spec{}, fmt.Errorf("a tunnel named %q already exists", name)
	}
	protocol, err := easytier.Protocol(n.EasyTierProtocol)
	if err != nil {
		return l3Spec{}, err
	}
	host := "0.0.0.0"
	if n.SetupFamily == "ipv6" {
		host = "::"
	}
	token := strings.TrimSpace(n.Token)
	if (side == sideIran) != n.EasyTierReverse {
		host = strings.Trim(strings.TrimSpace(n.PeerAddr), "[]")
		if host == "" || strings.ContainsAny(host, "/ \t\r\n") || strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return l3Spec{}, fmt.Errorf("enter a peer IP or hostname on the EasyTier dialing side")
		}
		if token == "" {
			return l3Spec{}, fmt.Errorf("enter the shared EasyTier network secret")
		}
	} else if token == "" {
		token = randomToken(64)
	}
	local, peer := strings.TrimSpace(n.LocalIP), strings.TrimSpace(n.PeerIP)
	if (local == "") != (peer == "") {
		return l3Spec{}, fmt.Errorf("enter both EasyTier tunnel addresses, or leave both empty to allocate a private subnet")
	}
	if local == "" {
		local, peer = freeL3Subnet(side)
	} else if owner := l3BlockOwner(local); owner != "" {
		return l3Spec{}, fmt.Errorf("the selected EasyTier subnet is already used by %s", owner)
	}
	spec := l3Spec{
		RoutedLinkSettings: n.RoutedLinkSettings,
		Name:               name, Side: side, Carrier: l3.CarrierEasyTier,
		Addr: net.JoinHostPort(host, port), Token: token, Iface: freeL3Iface(),
		LocalIP: local, PeerIP: peer, MTU: n.MTU, AutoMTU: n.AutoMTU, MSSClamp: n.MSSClamp,
		MaxConnections: n.MaxConnections, BandwidthMbps: n.BandwidthMbps,
	}
	spec.EasyTierNetwork = orDefault(strings.TrimSpace(n.EasyTierNetwork), "backpack")
	if n.EasyTierProtocol != "" {
		spec.EasyTierProtocol = protocol
	}
	findL3Preset(strings.ToLower(strings.TrimSpace(n.Preset))).apply(&spec)
	if side == sideIran {
		spec.Ports, spec.AcceptUDP = parsePorts(n.Ports), n.AcceptUDP
		if err := validatePortSpecs(spec.Ports); err != nil {
			return l3Spec{}, err
		}
	}
	return spec, nil
}

func routedLinkRunnable(carrier string) error {
	switch strings.ToLower(strings.TrimSpace(carrier)) {
	case l3.CarrierL2TPv3:
		for _, module := range []string{"l2tp_netlink", "l2tp_ip", "l2tp_eth"} {
			if err := exec.Command("modprobe", "-n", module).Run(); err != nil {
				return fmt.Errorf("this server's running kernel is missing %s; install its L2TPv3 kernel modules", module)
			}
		}
	case l3.CarrierEasyTier:
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		return easytier.Ensure(ctx)
	}
	return nil
}
