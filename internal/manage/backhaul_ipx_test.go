package manage

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/internal/backhaulpremium"
)

func TestBackhaulIPXSetupUsesOneProfileAndSubnet(t *testing.T) {
	mtu := 1270
	form := BackhaulForm{
		Name: "test-bh-ipx", Mode: "tun-ipx", IPXProfile: "udp", HealthPort: 28113,
		IranTunnel: "10.243.246.1/30", KharejTunnel: "10.243.246.2/30",
		Tuning: SetupTuning{Preset: "turbo", Value: &mtu},
	}
	c, err := backhaulIPXConfig(form, "192.0.2.1", "192.0.2.2")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := backhaulShare(form.Name, c).Encode()
	if err != nil {
		t.Fatal(err)
	}
	link, err := DecodeShareLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Native || link.Kind != "direct" || link.Port != "" || link.Tr != ctBackhaulIPX ||
		link.LocalIP != c.IranTunnel || link.PeerIP != c.KharejTunnel ||
		link.Profile != "udp" || link.HealthPort != 28113 || link.MTU != mtu {
		t.Fatalf("IPX Setup settings did not round trip: kind %s, transport %s, profile %s", link.Kind, link.Tr, link.Profile)
	}
	for _, role := range []string{"server", "client"} {
		c.Role = role
		remote, addr := "192.0.2.2", form.IranTunnel
		if role == "client" {
			remote, addr = "192.0.2.1", form.KharejTunnel
		}
		body, err := backhaulpremium.Build(form.Name, c, "eth-test")
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Tun struct {
				Encapsulation string
				LocalAddr     string `toml:"local_addr"`
				HealthPort    int    `toml:"health_port"`
				MTU           int
			}
			IPX struct {
				Mode, Profile string
				Destination   string `toml:"dst_ip"`
			}
		}
		if _, err := toml.Decode(string(body), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Tun.LocalAddr != addr || parsed.Tun.MTU != mtu || parsed.Tun.HealthPort != 28113 ||
			parsed.Tun.Encapsulation != "ipx" || parsed.IPX.Mode != role || parsed.IPX.Destination != remote ||
			parsed.IPX.Profile != "udp" {
			t.Fatalf("wrong IPX settings for %s: %+v", role, parsed)
		}
	}
}
