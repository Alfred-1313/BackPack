package l3

import (
	"fmt"
	"net"
	"strings"

	"github.com/backpack/backpack/internal/waterwall"
)

const (
	CarrierL2TPv3    = "l2tpv3"
	CarrierEasyTier  = "easytier"
	CarrierWaterWall = "waterwall-packet"
)

func PortlessLinkCarrier(carrier string) bool {
	return KernelCarrier(carrier) || carrier == CarrierL2TPv3 || carrier == CarrierWaterWall
}

func ExternalInterfaceCarrier(carrier string) bool {
	return PortlessLinkCarrier(carrier) || carrier == CarrierEasyTier
}

func (c *Config) validateRoutedLink() error {
	switch c.Carrier {
	case CarrierWaterWall:
		if !strings.Contains(c.LocalIP, "/") || net.ParseIP(hostPart(c.LocalIP)).To4() == nil ||
			net.ParseIP(hostPart(c.PeerIP)).To4() == nil {
			return fmt.Errorf("l3: WaterWall needs a local IPv4 CIDR and a peer IPv4 address")
		}
		if c.WaterWallProtocol == 0 {
			c.WaterWallProtocol = waterwall.DefaultProtocol
		}
		if err := waterwall.Validate(c.WaterWallProtocol, c.WaterWallWorkers); err != nil {
			return err
		}
		if c.AcceptUDP || c.GREKey != 0 {
			return fmt.Errorf("l3: WaterWall Packet Tunnel forwards TCP and has no GRE key")
		}
	case CarrierL2TPv3:
		if c.L2TPTunnelID == 0 || c.L2TPPeerTunnelID == 0 || c.L2TPSessionID == 0 || c.L2TPPeerSessionID == 0 {
			return fmt.Errorf("l3: L2TPv3 needs a local and peer tunnel ID and session ID, all nonzero")
		}
		if c.GREKey != 0 {
			return fmt.Errorf("l3: L2TPv3 uses tunnel and session IDs")
		}
	case CarrierEasyTier:
		c.EasyTierNetwork = strings.TrimSpace(c.EasyTierNetwork)
		if c.EasyTierNetwork == "" || len(c.EasyTierNetwork) > 128 || strings.ContainsAny(c.EasyTierNetwork, "\r\n\x00") {
			return fmt.Errorf("l3: EasyTier needs a network name of 1 to 128 bytes")
		}
		if strings.ContainsAny(c.Token, "\r\n\x00") {
			return fmt.Errorf("l3: the EasyTier network secret must be a single line")
		}
		if net.ParseIP(hostPart(c.LocalIP)).To4() == nil || net.ParseIP(c.PeerIP).To4() == nil {
			return fmt.Errorf("l3: EasyTier expects IPv4 tunnel addresses on both ends")
		}
		if c.GREKey != 0 || c.FEC.Data != 0 || c.FEC.Parity != 0 {
			return fmt.Errorf("l3: GRE keys and Backpack FEC do not apply to EasyTier")
		}
		if !strings.Contains(c.LocalIP, "/") {
			c.LocalIP += "/32"
		}
	}
	return nil
}
