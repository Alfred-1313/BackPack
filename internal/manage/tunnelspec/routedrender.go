package tunnelspec

import (
	"fmt"
	"strings"

	"github.com/backpack/backpack/internal/tunnel/l3"
)

func (s L3Spec) renderRoutedLink() string {
	var b strings.Builder
	direction := "direct"
	if s.Carrier == l3.CarrierEasyTier && s.EasyTierReverse {
		direction = "reverse"
	}
	fmt.Fprintf(&b, "# Backpack %s %s tunnel\n\n[l3]\n", s.Carrier, direction)
	mode := "dial"
	if (s.Side == SideKharej) != (direction == "reverse") {
		mode = "listen"
	}
	writeKV(&b, "mode", quote(mode))
	writeKV(&b, "carrier", quote(s.Carrier))
	writeKV(&b, "addr", quote(s.Addr))
	if s.Carrier == l3.CarrierL2TPv3 {
		if s.GRELocal != "" {
			writeKV(&b, "gre_local", quote(s.GRELocal))
		}
		writeKV(&b, "l2tp_tunnel_id", fmt.Sprint(s.L2TPTunnelID))
		writeKV(&b, "l2tp_peer_tunnel_id", fmt.Sprint(s.L2TPPeerTunnelID))
		writeKV(&b, "l2tp_session_id", fmt.Sprint(s.L2TPSessionID))
		writeKV(&b, "l2tp_peer_session_id", fmt.Sprint(s.L2TPPeerSessionID))
	} else if s.Carrier == l3.CarrierWaterWall {
		if s.GRELocal != "" {
			writeKV(&b, "gre_local", quote(s.GRELocal))
		}
		writeKV(&b, "waterwall_protocol", fmt.Sprint(s.WaterWallProtocol))
		writeKV(&b, "waterwall_workers", fmt.Sprint(s.WaterWallWorkers))
		if s.WaterWallPeerWorkers != nil {
			writeKV(&b, "waterwall_peer_workers", fmt.Sprint(*s.WaterWallPeerWorkers))
		}
		if s.WaterWallPeerMTU != 0 {
			writeKV(&b, "waterwall_peer_mtu", fmt.Sprint(s.WaterWallPeerMTU))
		}
	} else {
		writeKV(&b, "token", quote(s.Token))
		writeKV(&b, "easytier_network", quote(s.EasyTierNetwork))
		if s.EasyTierProtocol != "" {
			writeKV(&b, "easytier_protocol", quote(s.EasyTierProtocol))
		}
		if s.EasyTierReverse {
			writeKV(&b, "easytier_reverse", "true")
		}
	}
	writeKV(&b, "iface", quote(s.Iface))
	writeKV(&b, "local_ip", quote(s.LocalIP))
	writeKV(&b, "peer_ip", quote(s.PeerIP))
	if s.MTU > 0 {
		writeKV(&b, "mtu", fmt.Sprint(s.MTU))
	}
	if s.Preset != "" {
		writeKV(&b, "preset", quote(s.Preset))
	}
	if s.TxQueueLen > 0 {
		writeKV(&b, "txqueuelen", fmt.Sprint(s.TxQueueLen))
	}
	if s.MSSClamp != 0 {
		writeKV(&b, "mss_clamp", fmt.Sprint(s.MSSClamp))
	}
	if s.AutoMTU != nil {
		writeKV(&b, "auto_mtu", fmt.Sprint(*s.AutoMTU))
	}
	if s.Side == SideIran && len(s.Ports) > 0 {
		writeKV(&b, "ports", tomlList(s.Ports))
		writeKV(&b, "accept_udp", fmt.Sprint(s.AcceptUDP))
		if s.MaxConnections > 0 {
			writeKV(&b, "max_connections", fmt.Sprint(s.MaxConnections))
		}
		if s.BandwidthMbps > 0 {
			writeKV(&b, "bandwidth_mbps", fmt.Sprint(s.BandwidthMbps))
		}
	}
	return b.String()
}
