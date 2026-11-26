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
		CertFile: "/local/cert.pem", KeyFile: "/local/key.pem", Ports: "4242=443"}
	raw, err := shareLinkOf("dagger", "", config.Config{Dagger: cfg})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeShareLink(raw)
	want := daggerShare("dagger", cfg)
	want.V = 1
	if err != nil || !reflect.DeepEqual(got, want) || got.Ports != "" {
		t.Fatalf("Dagger peer link lost settings or copied local settings: %v", err)
	}
}
