package manage

import (
	"strings"
	"testing"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/daggerconnect"
	"github.com/backpack/backpack/internal/nativebackhaul"
)

func TestEveryDaggerPanelTransportHasAConnectionTest(t *testing.T) {
	rows := map[string]*connTestCase{}
	for _, c := range ctExtraCases() {
		if rows[c.tr] != nil {
			t.Fatalf("duplicate transport test %q", c.tr)
		}
		rows[c.tr] = c
	}
	for _, option := range nativebackhaul.Options() {
		c := rows[ctBackhaulPrefix+option.Value]
		if c == nil || c.kind != option.Direction || ctBackhaulLabel(c.tr) != option.Label {
			t.Errorf("%s has no matching Connection Test", option.Label)
		}
	}
}

func TestDaggerLegacyTestModes(t *testing.T) {
	rows := map[string]*connTestCase{}
	for _, c := range ctDaggerCases() {
		rows[c.tr] = c
	}
	for _, option := range daggerconnect.Options() {
		if option.Value == "tun" {
			for _, profile := range ctDaggerTUNProfiles {
				c := rows[ctDaggerTUNPrefix+profile]
				if c == nil || c.kind != "direct" {
					t.Errorf("Dagger TUN %s must test through its own direct tunnel", profile)
				}
			}
		} else if c := rows[ctDaggerPrefix+option.Value]; c == nil || c.kind != "reverse" {
			t.Errorf("%s has no reverse Connection Test", option.Label)
		}
	}
}

func TestDaggerRowsExplainMissingLicenseAndWrongPath(t *testing.T) {
	test := &ConnTestIran{link: ConnTestLink{Host: "192.0.2.1"}}
	if reason := test.daggerSkip(ctDaggerPrefix + "nova"); !strings.Contains(reason, "license") {
		t.Errorf("unexpected skip reason: %q", reason)
	}
	test.daggerLicense = "test-credential"
	if reason := test.daggerSkip(ctDaggerPrefix + "nova"); reason != "" {
		t.Errorf("unexpected IPv4 skip: %q", reason)
	}
	if reason := test.daggerSkip(ctDaggerPrefix + "dc6"); !strings.Contains(reason, "IPv6") {
		t.Errorf("DC6 must use IPv6, got %q", reason)
	}
	test.link.Host = "2001:db8::1"
	if reason := test.daggerSkip(ctDaggerPrefix + "dc6"); !strings.Contains(reason, "IPv4 address") {
		t.Errorf("DC6 requires the licensed IPv4 address, got %q", reason)
	}
	test.daggerIPv4 = "192.0.2.1"
	if reason := test.daggerSkip(ctDaggerPrefix + "dc6"); reason != "" {
		t.Errorf("unexpected DC6 skip: %q", reason)
	}
}

func TestDaggerPeerUsesTheAddressesForThisTest(t *testing.T) {
	job := ctExtraJob{Name: "bpct010203040506", Mode: "dagger-tun-gre"}
	near, far := ctDaggerTUNPair(job.Name)
	c := config.DaggerConfig{Flavor: "tun", TUNProfile: "gre", IranIP: "192.0.2.1",
		IranTunnelIP: near, KharejTunnelIP: far}
	if !ctDaggerTestEndpoint("192.0.2.1", job, c) {
		t.Fatal("rejected the matching TUN endpoints")
	}
	c.KharejTunnelIP = "198.18.0.1"
	if ctDaggerTestEndpoint("192.0.2.1", job, c) {
		t.Fatal("accepted a TUN address outside this test")
	}
}
