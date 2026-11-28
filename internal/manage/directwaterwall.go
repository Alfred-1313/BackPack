package manage

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/tunnel/l3"
	"github.com/backpack/backpack/internal/waterwall"
)

func (n NewDirectTunnel) waterWallSpec(side directSide, existing string) (l3Spec, error) {
	if err := validateSetupFamily(n.SetupFamily, ipv4SetupFamily); err != nil {
		return l3Spec{}, err
	}
	if n.WaterWallProtocol == 0 {
		n.WaterWallProtocol = waterwall.DefaultProtocol
	}
	if err := waterwall.Validate(n.WaterWallProtocol, n.WaterWallWorkers); err != nil {
		return l3Spec{}, err
	}
	if n.WaterWallPeerWorkers != nil {
		if err := waterwall.Validate(n.WaterWallProtocol, *n.WaterWallPeerWorkers); err != nil {
			return l3Spec{}, fmt.Errorf("peer settings: %w", err)
		}
	}
	if n.WaterWallPeerMTU != 0 && !waterWallMTUValid(n.WaterWallPeerMTU) {
		return l3Spec{}, fmt.Errorf("peer WaterWall MTU must be between 576 and 1500 bytes")
	}
	peer := net.ParseIP(strings.TrimSpace(n.PeerAddr)).To4()
	if peer == nil || !peer.IsGlobalUnicast() {
		return l3Spec{}, fmt.Errorf("enter a valid peer IPv4 address for WaterWall")
	}
	n.PeerAddr = peer.String()
	if err := n.normalizeWaterWallAddresses(); err != nil {
		return l3Spec{}, err
	}
	s, err := n.greSpecFor(side, l3.CarrierWaterWall, existing)
	if err != nil {
		return l3Spec{}, err
	}
	if n.AcceptUDP {
		return l3Spec{}, fmt.Errorf("this WaterWall configuration forwards TCP only")
	}
	if n.MTU == 0 {
		tuning := SetupTuning{Preset: orDefault(n.Preset, "turbo")}
		s.MTU, err = setupTuningValue(l3.CarrierWaterWall, tuning)
	} else if !waterWallMTUValid(n.MTU) {
		return l3Spec{}, fmt.Errorf("WaterWall MTU must be between 576 and 1500 bytes")
	}
	return s, err
}

func waterWallMTUValid(mtu int) bool {
	return mtu >= 576 && mtu <= waterwall.DefaultMTU
}

func (n *NewDirectTunnel) normalizeWaterWallAddresses() error {
	local, peer := strings.TrimSpace(n.LocalIP), strings.TrimSpace(n.PeerIP)
	if local == "" && peer == "" {
		return nil
	}
	if local == "" || peer == "" {
		return fmt.Errorf("enter both WaterWall tunnel addresses, or leave both blank for automatic allocation")
	}
	prefix, err := netip.ParsePrefix(local)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() {
		return fmt.Errorf("enter the local tunnel IPv4 address in CIDR form, such as 10.10.0.1/30")
	}
	peerAddr, err := netip.ParseAddr(peer)
	if strings.Contains(peer, "/") {
		var other netip.Prefix
		other, err = netip.ParsePrefix(peer)
		if err == nil && other.Bits() != prefix.Bits() {
			return fmt.Errorf("the two tunnel IPv4 prefixes must match")
		}
		peerAddr = other.Addr()
	}
	if err != nil || !peerAddr.Is4() || !peerAddr.IsGlobalUnicast() || !prefix.Contains(peerAddr) ||
		peerAddr == prefix.Addr() {
		return fmt.Errorf("enter two different WaterWall tunnel IPv4 addresses in the same subnet")
	}
	if prefix.Bits() < 31 {
		network := prefix.Masked().Addr().As4()
		broadcast := network
		for bit := prefix.Bits(); bit < 32; bit++ {
			broadcast[bit/8] |= 1 << (7 - bit%8)
		}
		base, last := netip.AddrFrom4(network), netip.AddrFrom4(broadcast)
		if prefix.Addr() == base || peerAddr == base || prefix.Addr() == last || peerAddr == last {
			return fmt.Errorf("enter usable WaterWall tunnel host addresses, excluding the network and broadcast addresses")
		}
	}
	n.LocalIP, n.PeerIP = prefix.String(), peerAddr.String()
	return nil
}

// SuggestWaterWallSubnet picks a matched pair from BackPack's private L3 pool.
func SuggestWaterWallSubnet(previous string) (localIP, peerIP string, err error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return "", "", err
	}
	var used []netip.Prefix
	for _, address := range addresses {
		if block, err := netip.ParsePrefix(address.String()); err == nil {
			used = append(used, block.Masked())
		}
	}
	for _, l := range existingL3Configs() {
		if block, err := netip.ParsePrefix(l.LocalIP); err == nil {
			used = append(used, block.Masked())
		} else if address, err := netip.ParseAddr(l.LocalIP); err == nil {
			used = append(used, netip.PrefixFrom(address, address.BitLen()))
		}
		if address, err := netip.ParseAddr(l.PeerIP); err == nil {
			used = append(used, netip.PrefixFrom(address, address.BitLen()))
		}
	}
	prior, _ := netip.ParsePrefix(strings.TrimSpace(previous))
	for _, octet := range rand.Perm(256) {
		network := netip.AddrFrom4([4]byte{10, 10, byte(octet), 0})
		block := netip.PrefixFrom(network, 30)
		if waterWallSubnetFree(block, prior, used) {
			return netip.PrefixFrom(network.Next(), 30).String(), network.Next().Next().String(), nil
		}
	}
	return "", "", fmt.Errorf("no free WaterWall IPv4 subnet in 10.10.0.0/16")
}

func waterWallSubnetFree(block, previous netip.Prefix, used []netip.Prefix) bool {
	if block.Overlaps(previous) {
		return false
	}
	for _, assigned := range used {
		if block.Overlaps(assigned) {
			return false
		}
	}
	return true
}

func ensureWaterWallTunnel(protocol int, iran bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	return waterwall.Ensure(ctx, iran, protocol)
}
