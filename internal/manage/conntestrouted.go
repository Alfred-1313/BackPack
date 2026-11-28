package manage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/easytier"
	"github.com/backpack/backpack/internal/tunnel/l3"
	"github.com/backpack/backpack/internal/waterwall"
)

func ctDirectPrerequisite(carrier, host string) string {
	if ctFamily(host) == "IPv6" {
		switch carrier {
		case l3.CarrierGRE, l3.CarrierIP6GRE, l3.CarrierEasyTier:
		default:
			return ctName(carrier) + " requires an IPv4 path; IPv6 is not supported"
		}
	}
	switch carrier {
	case l3.CarrierL2TPv3:
		if err := routedLinkRunnable(carrier); err != nil {
			return err.Error()
		}
	case l3.CarrierEasyTier:
		if _, err := easytier.Binary(); err != nil {
			return err.Error()
		}
	}
	return ""
}

// The Kharej address is learned from the coordinator. The Setup Link carries
// Iran's address and the actual kernel tunnel settings to the other end.
func (s *ConnTestIran) startPortlessCase(c *connTestCase, i int, carrier, host, preset string) error {
	if carrier == l3.CarrierGRE && ctFamily(host) == "IPv6" {
		carrier = l3.CarrierIP6GRE
	}
	block := ctFreeL3Block(i)
	c.peerIP = block + "2"
	c.l3 = l3Spec{
		Name: c.name, Side: sideIran, Carrier: carrier, Encap: "gre",
		Iface: fmt.Sprintf("bpt%d", i), LocalIP: block + "1/30", PeerIP: c.peerIP,
	}
	if carrier == l3.CarrierL2TPv3 {
		var form NewDirectTunnel
		if err := form.setL2TPIDs(); err != nil {
			return err
		}
		c.l3.RoutedLinkSettings = form.RoutedLinkSettings
	} else if carrier == l3.CarrierWaterWall {
		c.l3.WaterWallProtocol = waterwall.TestProtocol
		c.l3.MTU = 1400
	} else {
		c.l3.GREKey = freeGREKey()
	}
	findL3Preset(preset).apply(&c.l3)
	link, err := DecodeShareLink(pendingShareLinkFrom(c.l3, host, linkExtras{}))
	if err != nil {
		return fmt.Errorf("building the %s test link: %w", ctName(carrier), err)
	}
	link.Host, link.Name = strings.Trim(host, "[]"), c.name
	c.link = link
	return nil
}

func waterWallTestEnv(carrier string, port int) []string {
	if carrier != l3.CarrierWaterWall {
		return nil
	}
	return []string{fmt.Sprintf("BACKPACK_WATERWALL_TEST_PORT=%d", port)}
}

func ctWaterWallCore(ctx context.Context, iran bool) string {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := waterwall.Ensure(ctx, iran, waterwall.TestProtocol); err != nil {
		return err.Error()
	}
	return ""
}
