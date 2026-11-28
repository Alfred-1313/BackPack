package manage

import (
	"testing"

	"github.com/backpack/backpack/internal/tunnel/l3"
	"github.com/backpack/backpack/internal/waterwall"
)

func TestWaterWallSetupLinkMirrorsThePacketTunnel(t *testing.T) {
	form := NewDirectTunnel{
		Carrier: l3.CarrierWaterWall, Name: "waterwall-fixture", Side: "iran",
		GRELocal: "192.0.2.1", PeerAddr: "192.0.2.2", LocalIP: "10.213.252.1/30",
		PeerIP: "10.213.252.2", Ports: "27443=443", MTU: 1400,
	}
	form.WaterWallProtocol, form.WaterWallWorkers = 18, 3
	local, err := form.spec()
	if err != nil {
		t.Fatal(err)
	}
	if local.Addr != form.PeerAddr || len(local.Ports) != 1 || local.GREKey != 0 {
		t.Fatalf("unexpected Iran settings: %+v", local)
	}
	raw := pendingShareLinkFrom(local, form.GRELocal, linkExtras{})
	link, err := DecodeShareLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := MirrorForPeer(link).ToNewDirectTunnel().spec()
	if err != nil {
		t.Fatal(err)
	}
	table := decode(t, remote.Render()).L3
	if table.Mode != "listen" || table.Carrier != form.Carrier || table.Addr != form.GRELocal ||
		table.LocalIP != form.PeerIP+"/30" || table.PeerIP != "10.213.252.1" ||
		table.MTU != form.MTU || table.WaterWallProtocol != 18 || table.WaterWallWorkers != 3 || len(table.Ports) != 0 {
		t.Fatalf("unexpected Kharej settings: %+v", table)
	}
}

func TestWaterWallConnectionTestUsesProtocol17(t *testing.T) {
	iran := &ConnTestIran{}
	c := &connTestCase{tr: l3.CarrierWaterWall, kind: "direct", name: "ct-waterwall-fixture"}
	if err := iran.startPortlessCase(c, 99, c.tr, "192.0.2.1", "turbo"); err != nil {
		t.Fatal(err)
	}
	peer := MirrorForPeer(c.link)
	if c.l3.WaterWallProtocol != waterwall.TestProtocol || peer.WaterWallProtocol != waterwall.TestProtocol ||
		peer.ServerAddr != "192.0.2.1" {
		t.Fatal("both Connection Test endpoints must use the same IP protocol 17")
	}
}

func TestWaterWallLinkCarriesIndependentSettings(t *testing.T) {
	peerWorkers := 0
	form := NewDirectTunnel{
		Carrier: l3.CarrierWaterWall, Name: "waterwall-asymmetric-fixture", Side: "iran",
		GRELocal: "192.0.2.1", PeerAddr: "192.0.2.2", LocalIP: "10.213.253.1/30",
		PeerIP: "10.213.253.2/30", Ports: "27443=443", MTU: 1328,
	}
	form.WaterWallProtocol, form.WaterWallWorkers = 17, 3
	form.WaterWallPeerWorkers, form.WaterWallPeerMTU = &peerWorkers, 1452
	local, err := form.spec()
	if err != nil {
		t.Fatal(err)
	}
	cfg := decode(t, local.Render()).L3
	if cfg.WaterWallWorkers != 3 || cfg.MTU != 1328 || cfg.PeerIP != "10.213.253.2" ||
		cfg.WaterWallPeerWorkers == nil || *cfg.WaterWallPeerWorkers != 0 || cfg.WaterWallPeerMTU != 1452 {
		t.Fatalf("Iran config lost its own settings or the peer override: %+v", cfg)
	}
	link, err := DecodeShareLink(pendingShareLinkFrom(local, form.GRELocal, linkExtras{}))
	if err != nil {
		t.Fatal(err)
	}
	peer, err := MirrorForPeer(link).ToNewDirectTunnel().spec()
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, peer.Render()).L3
	if got.MTU != 1452 || got.WaterWallWorkers != 0 || got.WaterWallProtocol != 17 ||
		got.LocalIP != "10.213.253.2/30" || got.PeerIP != "10.213.253.1" {
		t.Fatalf("Kharej config did not get its own setup values: %+v", got)
	}
	if err := waterwall.Validate(6, 3); err == nil {
		t.Fatal("protocol 6 would capture native TCP")
	}
}

func TestWaterWallRandomSubnetUsesUsablePair(t *testing.T) {
	first, _, err := SuggestWaterWallSubnet("")
	if err != nil {
		t.Fatal(err)
	}
	local, peer, err := SuggestWaterWallSubnet(first)
	if err != nil {
		t.Fatal(err)
	}
	if local == first {
		t.Fatal("Random reused the previous subnet")
	}
	form := NewDirectTunnel{LocalIP: local, PeerIP: peer}
	if err := form.normalizeWaterWallAddresses(); err != nil {
		t.Fatal(err)
	}
}

func TestWaterWallSetupReplayMatchesOnlyTheSamePeer(t *testing.T) {
	form := NewDirectTunnel{
		Carrier: l3.CarrierWaterWall, Name: "waterwall-replay-fixture", Side: "kharej",
		PeerAddr: "192.0.2.1", LocalIP: "10.213.251.2/30", PeerIP: "10.213.251.1",
		MTU: 1400,
	}
	form.WaterWallProtocol, form.WaterWallWorkers = 17, 2
	spec, err := form.spec()
	if err != nil {
		t.Fatal(err)
	}
	have := decode(t, spec.Render()).L3
	if !sameWaterWallPeer(have, spec) {
		t.Fatal("replaying the same WaterWall setup should reuse the listener")
	}
	for _, tc := range []struct {
		name   string
		change func(*l3Spec)
	}{
		{"another remote", func(want *l3Spec) { want.Addr = "192.0.2.2" }},
		{"another address on the same subnet", func(want *l3Spec) { want.PeerIP = "10.213.251.3" }},
		{"different outer protocol", func(want *l3Spec) { want.WaterWallProtocol = 136 }},
		{"different workers", func(want *l3Spec) { want.WaterWallWorkers++ }},
		{"different MTU", func(want *l3Spec) { want.MTU -= 200 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := spec
			tc.change(&want)
			if sameWaterWallPeer(have, want) {
				t.Fatal("a matching subnet alone must not reuse this listener")
			}
		})
	}
}
