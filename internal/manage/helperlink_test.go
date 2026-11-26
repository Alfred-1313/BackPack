package manage

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/nativebackhaul"
)

func TestImportedSetupLinksAreCompact(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdefghijklmnopqrstuv"))
	for _, option := range nativebackhaul.Options() {
		t.Run(option.Value, func(t *testing.T) {
			cfg := config.BackhaulConfig{Native: true, Role: "server", Mode: option.Value,
				IranHost: "192.0.2.1", Port: 7280, Token: key, Ports: []string{"443=8443"}}
			if option.Value == "tun-tcp" {
				cfg.IranTunnel, cfg.KharejTunnel = "10.21.52.1/30", "10.21.52.2/30"
			}
			link := backhaulShare("FL", cfg)
			info, err := importedSetupLink(link)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(info.Link, "backpack://2.") || info.Apply != "sudo backpack link apply '"+info.Link+"'" {
				t.Fatal("the imported setup does not use the standard link command")
			}
			legacy := backhaulPeerToken("FL", cfg)
			if len(info.Link) >= len(legacy)/2 || (option.Value != "tun-tcp" && len(info.Link) > 100) {
				t.Fatalf("link length %d, legacy length %d", len(info.Link), len(legacy))
			}
			link.V = 1
			got, err := DecodeShareLink(FindSetupLink(info.Apply))
			if err != nil || !reflect.DeepEqual(link, got) {
				t.Fatalf("paired link settings did not round trip: %v", err)
			}
		})
	}
}

func TestDaggerSetupLinkOnlyCarriesPeerSettings(t *testing.T) {
	cfg := config.DaggerConfig{Role: "server", Flavor: "https", IranIP: "192.0.2.1", Port: 443,
		License: "test-license", HTTPHost: "example.net", HTTPPath: "/tunnel",
		ConnectionPool: 12,
		CertFile:       "/local/cert.pem", KeyFile: "/local/key.pem", Ports: "4242=443"}
	raw, err := shareLinkOf("dagger", "", config.Config{Dagger: cfg})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeShareLink(raw)
	want := daggerShare("dagger", cfg)
	want.V = 1
	if err != nil || !reflect.DeepEqual(got, want) || got.Ports != "" || got.TunnelLanes != 12 {
		t.Fatalf("Dagger peer link lost settings or copied local settings: %v", err)
	}
}

func TestNativeMuxLinkCarriesBackupAndHeartbeat(t *testing.T) {
	for _, mode := range []string{"xtcpmux", "xwsmux"} {
		c := config.BackhaulConfig{Native: true, Role: "server", Mode: mode, IranHost: "192.0.2.1", Port: 7280,
			Token: "fixture", Mux: &config.BackhaulMuxOptions{
				Backups: []string{"198.51.100.3:8443"}, HeartbeatSeconds: 3, TimeoutSeconds: 12,
				ProxyProtocol: true, MaxConnections: 4, BandwidthMbps: 15,
			}}
		raw, err := shareLinkWith("peer", "", config.Config{Backhaul: c}, linkExtras{restartHours: 6})
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeShareLink(raw)
		want := backhaulShare("peer", config.BackhaulConfig{Native: true, Mode: mode, IranHost: c.IranHost,
			Port: c.Port, Token: c.Token, Mux: &config.BackhaulMuxOptions{
				Backups: c.Mux.Backups, HeartbeatSeconds: 3, TimeoutSeconds: 12,
			}})
		want.V, want.RestartHours = 1, 6
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: the peer link did not carry the intended settings: %v", mode, err)
		}
	}
}

func TestNativeDaggerSetupLinkCarriesHTTPRoute(t *testing.T) {
	c := config.BackhaulConfig{Native: true, Role: "server", Mode: "dagger-https", IranHost: "192.0.2.1",
		Port: 443, Token: "fixture", HTTPHost: "relay.example.test", HTTPPath: "/api/v2"}
	info, err := importedSetupLink(backhaulShare("peer", c))
	if err != nil {
		t.Fatal(err)
	}
	l, err := DecodeShareLink(info.Link)
	if err != nil || l.HelperHost != c.HTTPHost || l.HelperPath != c.HTTPPath {
		t.Fatalf("native Dagger route did not round trip: %v", err)
	}
}
