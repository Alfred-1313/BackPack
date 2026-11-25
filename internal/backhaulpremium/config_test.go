package backhaulpremium

import (
	"encoding/base64"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
)

func TestBackhaulRendersTheNativeListenerAndDialer(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, mode := range []string{"xtcpmux", "xwsmux", "tun-tcp", "tun-ipx"} {
		t.Run(mode, func(t *testing.T) {
			c := config.BackhaulConfig{Mode: mode, Token: key, Port: 29001,
				IranHost: "192.0.2.10", KharejIP: "198.51.100.2",
				IranTunnel: "10.145.19.1/30", KharejTunnel: "10.145.19.2/30",
				IPXProfile: "icmp", HealthPort: 28000}
			for _, role := range []string{"server", "client"} {
				c.Role = role
				rendered, err := Build("integration-test", c, "eth0")
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]map[string]any
				if _, err := toml.Decode(string(rendered), &doc); err != nil {
					t.Fatal(err)
				}
				if !IsTUN(mode) {
					if doc["transport"]["type"] != mode || doc["mux"]["mux_version"] != int64(2) {
						t.Fatalf("unexpected Mux config: %s", rendered)
					}
				} else {
					addr := c.IranTunnel
					if role == "client" {
						addr = c.KharejTunnel
					}
					if doc["transport"]["type"] != "tun" || doc["tun"]["local_addr"] != addr {
						t.Fatalf("incorrect TUN endpoint: %s", rendered)
					}
				}
				if mode == "tun-ipx" {
					if doc["ipx"]["mode"] != role || doc["ipx"]["profile"] != "icmp" ||
						doc["security"]["psk"] != key || doc["tuning"]["batch_size"] != int64(32) ||
						doc["listener"] != nil || doc["dialer"] != nil {
						t.Fatalf("unexpected IPX config: %s", rendered)
					}
				} else if (doc["listener"] != nil) != (role == "server") ||
					(doc["dialer"] != nil) != (role == "client") {
					t.Fatalf("wrong transport direction: %s", rendered)
				}
			}
		})
	}
}

func TestBackhaulAcceptsThePrivateConnectionTestForward(t *testing.T) {
	c := config.BackhaulConfig{Role: "server", Mode: "xtcpmux", IranHost: "192.0.2.1", Port: 8443,
		Token: "test-token", Ports: []string{"127.0.0.1:22345=127.0.0.1:22346"}}
	if _, err := Build("test", c, ""); err != nil {
		t.Fatal(err)
	}
	c.Role = "client"
	if _, err := Build("test", c, ""); err == nil {
		t.Fatal("the peer must not install Iran's forwarded ports")
	}
}
