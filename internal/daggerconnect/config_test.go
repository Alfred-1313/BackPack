package daggerconnect

import (
	"encoding/json"
	"testing"

	"github.com/backpack/backpack/config"
)

func TestDaggerOfficialConfigShapes(t *testing.T) {
	for _, option := range Options() {
		for _, role := range []string{"server", "client"} {
			t.Run(option.Value+"/"+role, func(t *testing.T) {
				form := config.DaggerConfig{
					Role: role, Flavor: option.Value, IranIP: "192.0.2.3", KharejIP: "198.51.100.7",
					IranIPv6: "2001:db8::3", Port: 28755, License: "fixture-psk",
					Ports: "4400, udp:5553=53", HTTPHost: "tunnel.example", HTTPPath: "/exchange",
					CertFile: "/etc/certs/server.crt", KeyFile: "/etc/certs/server.key",
					TUNProfile: "tcp", TUNPort: 443, IranTunnelIP: "10.201.8.1", KharejTunnelIP: "10.201.8.2",
				}
				result, err := Build("test-dagger", form)
				if err != nil {
					t.Fatal(err)
				}
				var body struct {
					Mode      string
					Transport string
					Listeners []struct {
						Addr string
						Maps []portMap
					}
					Paths []struct {
						Addr string
						IPv6 string `json:"ipv6_addr"`
					}
					TUN     map[string]any
					IPX     map[string]any
					Quantum map[string]any
				}
				if err := json.Unmarshal(result, &body); err != nil || body.Mode != role {
					t.Fatal("wrong endpoint configuration")
				}
				if role == "server" {
					if len(body.Listeners) != 1 || len(body.Listeners[0].Maps) != 2 || len(body.Paths) != 0 {
						t.Fatal("server should own the bindings")
					}
				} else if len(body.Paths) != 1 || body.Paths[0].Addr != "192.0.2.3:28755" || len(body.Listeners) != 0 {
					t.Fatal("client should dial Iran")
				}
				switch option.Value {
				case "quantum-gaming":
					if body.Transport != "quantum" || body.Quantum["profile"] != "gaming" {
						t.Fatal("gaming is a Quantum profile")
					}
				case "tun":
					inner, outer := form.IranTunnelIP, form.IranIP
					if role == "client" {
						inner, outer = form.KharejTunnelIP, form.KharejIP
					}
					if body.TUN["local_addr"] != inner || body.IPX["listen_ip"] != outer {
						t.Fatal("addresses must belong to the local side")
					}
				case "dc6":
					if role == "server" && body.Listeners[0].Addr != "[2001:db8::3]:28755" ||
						role == "client" && body.Paths[0].IPv6 != "[2001:db8::3]:28755" {
						t.Fatal("DC6 traffic must use the IPv6 endpoint")
					}
				}
			})
		}
	}
}
