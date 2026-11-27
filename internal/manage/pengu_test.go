package manage

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/internal/pengutunnel"
)

func TestPenguSetupAndConnectionTestUseTheSameModes(t *testing.T) {
	for _, row := range ctPenguCases() {
		t.Run(row.tr, func(t *testing.T) {
			mode, direction, ok := ctPenguMode(row.tr)
			if !ok || row.kind != direction || !row.extra {
				t.Fatalf("invalid PenguTunnel Connection Test row: %s", row.tr)
			}
			form := PenguForm{Name: "pengu-test", Transport: mode.ID, Direction: direction, Family: "ipv4",
				IranHost: "192.0.2.1", KharejHost: "192.0.2.2", Ports: "8443=443,udp:51820",
				HealthPort: 26880, Preset: "aggressive", Workers: 4, Cipher: "chacha20-poly1305"}
			if !mode.IPX {
				form.Port = 26881
			}
			if mode.TUN {
				form.IranTunnel, form.KharejTunnel = "10.202.12.1/30", "10.202.12.2/30"
			}
			if mode.ID == "icmp" {
				code := 0
				form.ICMPType = &code
			}
			if mode.ID == "tcpfwd" {
				form.Header = "PGprotocol-NUM2"
				form.Window = 16
			}
			if mode.ID == "tls" {
				form.TLSName = "tunnel.example.net"
			}
			iran, err := penguFromForm(form)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := penguShare(form.Name, iran).Encode()
			if err != nil {
				t.Fatal(err)
			}
			l, err := DecodeShareLink(raw)
			if err != nil {
				t.Fatal(err)
			}
			kharej := penguPeer(l, mode.ID, form.Port)
			expect := iran
			expect.Side, expect.Ports = "kharej", nil
			if !reflect.DeepEqual(expect, kharej) {
				t.Fatal("setup link dropped a PenguTunnel setting")
			}
			if strings.Contains(raw, "test-local-license") || !strings.HasPrefix(raw, "backpack://2.") {
				t.Fatal("expected a compact setup link")
			}
			for _, c := range []struct{ client, listens bool }{{false, direction == "reverse"}, {true, direction == "direct"}} {
				cfg := iran
				if c.client {
					cfg = kharej
				}
				body, err := pengutunnel.Build(form.Name, cfg, "eth0", "test-local-license")
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]map[string]any
				if _, err = toml.Decode(string(body), &doc); err != nil {
					t.Fatal(err)
				}
				tr := doc["transport"]
				if tr["type"] != mode.Carrier {
					t.Fatalf("carrier = %q, expected %q", tr["type"], mode.Carrier)
				}
				if mode.IPX {
					if _, found := tr["listen"]; found || doc["ipx"]["profile"] != mode.ID {
						t.Fatal("IPX must use the selected raw profile")
					}
				} else if c.listens {
					if tr["listen"] != "0.0.0.0:"+strconv.Itoa(int(form.Port)) {
						t.Fatal("listener is on the wrong side")
					}
				} else if tr["remote"] == nil {
					t.Fatal("missing dial target")
				}
				if _, found := doc["license"]; found != c.client {
					t.Fatal("license must be in the Kharej engine configuration only")
				}
				if _, found := doc["forward"]; found == c.client {
					t.Fatal("forwarded ports belong only to Iran")
				}
			}
		})
	}
}
