package manage

import (
	"strings"
	"testing"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

func TestGRETAPPanelCreateAndPeer(t *testing.T) {
	if !offeredOnPanel(l3.CarrierGRETAP) || strings.Contains(strings.Join(carrierNames(), ","), l3.CarrierGRETAP) {
		t.Fatal("GRETAP belongs in the panel's creation menu")
	}
	n := NewDirectTunnel{
		Side: "iran", Carrier: l3.CarrierGRETAP, Name: "gretap-panel-fixture",
		PeerAddr: "203.0.113.44", GRELocal: "198.51.100.31",
		LocalIP: "10.212.237.1/30", PeerIP: "10.212.237.2",
		Preset: PresetAggressive, MTU: 1320,
	}
	spec, err := n.spec()
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := directBodyFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, body)
	if cfg.L3.Carrier != l3.CarrierGRETAP || cfg.L3.GREKey != 0 || len(cfg.L3.Ports) != 0 {
		t.Fatalf("the unkeyed, routed GRETAP link did not round trip: %+v", cfg.L3)
	}
	if cfg.L3.TxQueueLen != 16384 || cfg.L3.MTU != 1320 {
		t.Fatal("GRETAP lost the preset or optional MTU in its config")
	}
	link, err := shareLinkOf(spec.Name, n.GRELocal, cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	form := MirrorForPeer(decoded).ToNewDirectTunnel()
	form.Name = "gretap-panel-peer-fixture"
	peer, err := form.spec()
	if err != nil {
		t.Fatal(err)
	}
	if peer.Carrier != l3.CarrierGRETAP || peer.Addr != n.GRELocal || peer.LocalIP != n.PeerIP+"/30" || peer.PeerIP != "10.212.237.1" {
		t.Fatalf("the peer did not get the mirrored GRETAP link: %+v", peer)
	}
	if peer.TxQueueLen != cfg.L3.TxQueueLen || peer.MTU != cfg.L3.MTU {
		t.Fatal("GRETAP lost the performance settings on the peer")
	}
	settings := directSettingsFrom(n.Name, cfg.L3)
	if len(settings.Carriers) != 1 || settings.Carriers[0]["value"] != l3.CarrierGRETAP {
		t.Fatalf("GRETAP can only be edited as GRETAP: %v", settings.Carriers)
	}
	n.PeerAddr = "2001:db8::1"
	if _, err := n.spec(); err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Fatalf("expected GRETAP to reject an IPv6 underlay, got %v", err)
	}
}

func TestGRETAPDuplicateCheckKeepsRoutedGRESeparate(t *testing.T) {
	const addr = "203.0.113.44"
	tunnels := map[string]config.L3Config{
		"gre": {Carrier: l3.CarrierGRE, Addr: addr, GREKey: 0},
		"tap": {Carrier: l3.CarrierGRETAP, Addr: addr, GREKey: 0},
	}
	if got := greJoiningIn(tunnels, l3.CarrierGRETAP, addr, 0); got != "tap" {
		t.Fatalf("GRETAP matched %q", got)
	}
	if got := greJoiningIn(tunnels, l3.CarrierGRE, addr, 0); got != "gre" {
		t.Fatalf("routed GRE matched %q", got)
	}
}
