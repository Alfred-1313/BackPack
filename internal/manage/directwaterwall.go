package manage

import (
	"context"
	"fmt"
	"net"
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
	peer := net.ParseIP(strings.TrimSpace(n.PeerAddr)).To4()
	if peer == nil || !peer.IsGlobalUnicast() {
		return l3Spec{}, fmt.Errorf("enter a valid peer IPv4 address for WaterWall")
	}
	n.PeerAddr = peer.String()
	s, err := n.greSpecFor(side, l3.CarrierWaterWall, existing)
	if err != nil {
		return l3Spec{}, err
	}
	if net.ParseIP(hostOf(s.LocalIP)).To4() == nil || net.ParseIP(s.PeerIP).To4() == nil {
		return l3Spec{}, fmt.Errorf("WaterWall requires IPv4 addresses inside the tunnel")
	}
	if n.AcceptUDP {
		return l3Spec{}, fmt.Errorf("this WaterWall configuration forwards TCP only")
	}
	if !strings.Contains(s.LocalIP, "/") {
		return l3Spec{}, fmt.Errorf("the local tunnel IPv4 address needs a CIDR prefix")
	}
	if n.MTU == 0 {
		tuning := SetupTuning{Preset: orDefault(n.Preset, "turbo")}
		s.MTU, err = setupTuningValue(l3.CarrierWaterWall, tuning)
	} else if n.MTU < 576 || n.MTU > waterwall.DefaultMTU {
		return l3Spec{}, fmt.Errorf("WaterWall MTU must be between 576 and 1500 bytes")
	} else {
		s.MTU = n.MTU
	}
	return s, err
}

func ensureWaterWallTunnel(protocol int, iran bool) error {
	if protocol == waterwall.TestProtocol {
		return fmt.Errorf("WaterWall %s does not pass TCP payload over protocol 17; choose 18 or 136", waterwall.Version)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	return waterwall.Ensure(ctx, iran)
}
