package manage

import (
	"testing"

	"github.com/backpack/backpack/internal/nativebackhaul"
	"github.com/backpack/backpack/internal/omni"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

func TestConnectionTestCoversEveryPanelTransport(t *testing.T) {
	tested := make(map[string]bool)
	add := func(kind, transport string) {
		key := kind + "/" + transport
		if tested[key] {
			t.Errorf("duplicate Connection Test case for %s", key)
		}
		tested[key] = true
	}
	for _, transport := range connTestReverse {
		add("reverse", transport)
	}
	for _, transport := range connTestDirect {
		add("direct", transport)
	}
	for _, c := range ctExtraCases() {
		add(c.kind, c.tr)
	}

	assertTest := func(kind, transport string) {
		key := kind + "/" + transport
		if !tested[key] {
			t.Errorf("Add a tunnel offers %s but Connection Test does not", key)
		}
	}
	for _, family := range TransportFamilies() {
		for _, option := range family.Entries {
			assertTest("reverse", option.Value)
		}
	}
	for _, option := range PanelDirectCarriers() {
		assertTest("direct", option["value"])
	}
	for _, option := range nativebackhaul.Options() {
		assertTest(option.Direction, ctBackhaulPrefix+option.Value)
	}
	for _, option := range omni.Options() {
		assertTest(option["direction"], ctOmniPrefix+option["value"])
	}
	assertTest("direct", ctGostDirect)
	assertTest("direct", "gost-direct-v2")
}

func TestConnectionTestRoutedLinksMirrorTheKernelSettings(t *testing.T) {
	for _, carrier := range []string{l3.CarrierGRETAP, l3.CarrierL2TPv3} {
		t.Run(carrier, func(t *testing.T) {
			test := &ConnTestIran{}
			c := &connTestCase{kind: "direct", tr: carrier, name: "ct-routed-test"}
			if err := test.startPortlessCase(c, 2, carrier, "192.0.2.10", PresetTurbo); err != nil {
				t.Fatal(err)
			}
			peer := MirrorForPeer(c.link)
			if peer.Carrier != carrier || peer.ServerAddr != "192.0.2.10" ||
				peer.Side != "kharej" || peer.TunnelPort != "" || peer.Token != "" ||
				peer.LocalIP != c.l3.PeerIP+"/30" || peer.PeerIP != hostOf(c.l3.LocalIP) {
				t.Fatalf("unexpected %s peer settings", carrier)
			}
			if carrier == l3.CarrierGRETAP && (c.l3.GREKey == 0 || peer.GREKey != c.l3.GREKey) {
				t.Fatal("GRETAP key must match on both sides")
			}
			if carrier == l3.CarrierL2TPv3 &&
				(c.l3.L2TPTunnelID == 0 || c.l3.L2TPPeerTunnelID == 0 ||
					peer.L2TPTunnelID != c.l3.L2TPPeerTunnelID || peer.L2TPPeerTunnelID != c.l3.L2TPTunnelID ||
					peer.L2TPSessionID != c.l3.L2TPPeerSessionID || peer.L2TPPeerSessionID != c.l3.L2TPSessionID) {
				t.Fatal("L2TPv3 tunnel and session IDs must swap at the peer")
			}
		})
	}
}
