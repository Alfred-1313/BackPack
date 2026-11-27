package manage

import (
	"github.com/BurntSushi/toml"
	"testing"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

func TestRoutedLinkSetupFromPeer(t *testing.T) {
	for _, carrier := range []string{l3.CarrierL2TPv3, l3.CarrierEasyTier} {
		t.Run(carrier, func(t *testing.T) {
			f := NewDirectTunnel{
				Name: "vortex-setup-fixture", Side: "kharej", Carrier: carrier,
				LocalIP: "10.209.140.2/30", PeerIP: "10.209.140.1",
				Preset: PresetAggressive, MTU: 1320,
			}
			switch carrier {
			case l3.CarrierL2TPv3:
				f.PeerAddr, f.GRELocal = "198.51.100.5", "203.0.113.5"
				f.RoutedLinkSettings = config.RoutedLinkSettings{
					L2TPTunnelID: 93001, L2TPPeerTunnelID: 93002,
					L2TPSessionID: 94001, L2TPPeerSessionID: 94002,
				}
			case l3.CarrierEasyTier:
				f.TunnelPort, f.EasyTierNetwork, f.Token = "38577", "vortex-setup-fixture", "private-test-secret"
			}
			spec, err := f.spec()
			if err != nil {
				t.Fatal(err)
			}
			_, body, err := directBodyFromSpec(spec)
			if err != nil {
				t.Fatal(err)
			}
			cfg := decode(t, body)
			raw, err := shareLinkOf(f.Name, "203.0.113.5", cfg)
			if err != nil {
				t.Fatal(err)
			}
			link, err := DecodeShareLink(raw)
			if err != nil {
				t.Fatal(err)
			}
			peer := MirrorForPeer(link).ToNewDirectTunnel()
			mirror, err := peer.spec()
			if err != nil {
				t.Fatal(err)
			}
			_, peerBody, err := directBodyFromSpec(mirror)
			if err != nil {
				t.Fatal(err)
			}
			got := decode(t, peerBody).L3
			if got.Mode != "dial" || got.Carrier != carrier || got.LocalIP != "10.209.140.1/30" || got.PeerIP != "10.209.140.2" {
				t.Fatalf("unexpected peer: %s %s %s to %s", got.Mode, got.Carrier, got.LocalIP, got.PeerIP)
			}
			if cfg.L3.TxQueueLen != 16384 || got.TxQueueLen != cfg.L3.TxQueueLen || got.MTU != cfg.L3.MTU || got.MTU != 1320 {
				t.Fatal("the preset's transmit queue or the optional MTU did not reach both configs")
			}
			switch carrier {
			case l3.CarrierL2TPv3:
				if got.Addr != "203.0.113.5" || got.L2TPTunnelID != f.L2TPPeerTunnelID ||
					got.L2TPPeerTunnelID != f.L2TPTunnelID || got.L2TPSessionID != f.L2TPPeerSessionID ||
					got.L2TPPeerSessionID != f.L2TPSessionID {
					t.Fatal("peer L2TPv3 addresses or identifiers did not mirror")
				}
			case l3.CarrierEasyTier:
				if got.Addr != "203.0.113.5:38577" || got.EasyTierNetwork != f.EasyTierNetwork || got.Token != f.Token {
					t.Fatal("EasyTier peer settings did not mirror")
				}
			}
		})
	}
}

func TestEasyTierSetupMirrorsDirectionAndProtocol(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, protocol := range []string{"", "tcp", "udp", "ws", "wss"} {
			form := NewDirectTunnel{
				Carrier: l3.CarrierEasyTier, Side: "iran", Name: "easytier-transport-fixture",
				PeerAddr: "203.0.113.50", Token: "fixture-secret", TunnelPort: "20081",
				LocalIP: "10.244.246.1/30", PeerIP: "10.244.246.2", Ports: "443",
			}
			form.EasyTierReverse, form.EasyTierProtocol = reverse, protocol
			near, err := form.spec()
			if err != nil {
				t.Fatal(err)
			}
			nearMode, farMode := "dial", "listen"
			if reverse {
				nearMode, farMode = "listen", "dial"
			}
			var cfg config.Config
			if _, err := toml.Decode(near.Render(), &cfg); err != nil {
				t.Fatal(err)
			}
			link, err := DecodeShareLink(pendingShareLinkFrom(near, "192.0.2.90", linkExtras{}))
			if err != nil {
				t.Fatal(err)
			}
			peer := MirrorForPeer(link).ToNewDirectTunnel()
			far, err := peer.spec()
			if err != nil {
				t.Fatal(err)
			}
			var other config.Config
			if _, err := toml.Decode(far.Render(), &other); err != nil {
				t.Fatal(err)
			}
			if cfg.L3.Mode != nearMode || other.L3.Mode != farMode || other.L3.EasyTierProtocol != protocol ||
				other.L3.EasyTierReverse != reverse || len(cfg.L3.Ports) == 0 || len(other.L3.Ports) > 0 {
				t.Fatalf("wrong paired EasyTier config for reverse %t, protocol %q", reverse, protocol)
			}
			if reverse && far.Addr != "192.0.2.90:20081" {
				t.Fatal("Kharej must dial the Iran address carried by the reverse setup link")
			}
			if directSpecFrom(near.Name, cfg.L3).Side != sideIran {
				t.Fatal("editing an Iran listener would have moved it to Kharej")
			}
		}
	}
}
