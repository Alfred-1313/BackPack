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
		if option.Value == "http" || option.Value == "https" {
			if rows[ctDaggerPrefix+option.Value] != nil {
				t.Errorf("%s is already represented by its native BackPack test", option.Label)
			}
			continue
		}
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

func TestConnectionTestListsQuantumWithOrWithoutLicense(t *testing.T) {
	reverse, direct := connTestReverse, connTestDirect
	connTestReverse, connTestDirect = nil, nil
	defer func() { connTestReverse, connTestDirect = reverse, direct }()

	for _, license := range []string{"", "fixture-psk"} {
		s, _, err := StartConnTestIran(ConnTestOptions{
			Host: "127.0.0.1", ExtraTransports: true, DaggerLicense: license,
		})
		if err != nil {
			t.Fatal(err)
		}
		rows := map[string]string{}
		for _, c := range s.cases {
			rows[c.tr] = c.skip
		}
		for _, tr := range []string{"dagger-xhttp", "dagger-xhttps", "dagger-quantum", "dagger-quantum+", "dagger-quantum-gaming", "dagger-nova", "dagger-tun-gre"} {
			reason, listed := rows[tr]
			switch {
			case !listed:
				t.Errorf("%s disappeared from the test list", tr)
			case !s.root:
				if !strings.Contains(reason, "root") {
					t.Errorf("%s should explain its root requirement: %q", tr, reason)
				}
			case license == "":
				if !strings.Contains(reason, "license") {
					t.Errorf("%s should ask for a license: %q", tr, reason)
				}
			default:
				if reason != "" {
					t.Errorf("%s should be eligible for real tests: %q", tr, reason)
				}
			}
		}
		settings, err := unGzipB64(ctConfig(s.link))
		s.Close()
		if err != nil || (license != "" && strings.Contains(string(settings), license)) {
			t.Fatal("the public test settings should not contain the DaggerConnect PSK")
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
