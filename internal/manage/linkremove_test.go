package manage

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/backpack/backpack/config"
)

func TestRemovalLinkFindsRenamedKharejAndRefusesAmbiguity(t *testing.T) {
	near := config.Config{Server: config.ServerConfig{BindAddr: ":2083", Token: "test-shared-token"}}
	far := config.Config{Client: config.ClientConfig{RemoteAddr: "192.0.2.1:2083", Token: near.Server.Token}}
	other := far
	other.Client.RemoteAddr = "192.0.2.1:2087"
	id, side := peerRemovalIdentity(Tunnel{Role: "server"}, near)
	if id == "" || side != "iran" {
		t.Fatal("missing Iran identity")
	}
	body, err := json.Marshal(removalLink{Name: "Original", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	raw := removalScheme + base64.RawURLEncoding.EncodeToString(body)
	link, err := parseRemovalLink(raw)
	if err != nil || strings.Contains(string(body), near.Server.Token) {
		t.Fatalf("fingerprint link: %v", err)
	}
	tunnels := []Tunnel{{Name: "Original", Role: "server"}, {Name: "Different", Role: "client"}, {Name: "Renamed", Role: "client"}}
	configs := map[string]config.Config{"Original": near, "Different": other, "Renamed": far}
	load := func(name string) (config.Config, error) { return configs[name], nil }
	match, err := findRemovalPeer(link, "", tunnels, load)
	if err != nil || match.Name != "Renamed" {
		t.Fatalf("match = %q, %v", match.Name, err)
	}
	for _, name := range []string{"Original", "Different"} {
		if _, err := findRemovalPeer(link, name, tunnels, load); err == nil {
			t.Fatalf("selected unrelated tunnel %s", name)
		}
	}
	configs["Duplicate"] = far
	tunnels = append(tunnels, Tunnel{Name: "Duplicate", Role: "client"})
	if _, err := findRemovalPeer(link, "", tunnels, load); err == nil {
		t.Fatal("accepted multiple matching peers")
	}
	match, err = findRemovalPeer(link, "Renamed", tunnels, load)
	if err != nil || match.Name != "Renamed" {
		t.Fatalf("explicit match = %q, %v", match.Name, err)
	}
}

func TestRemovalLinkIdentifiesGREAndDaggerEndpoints(t *testing.T) {
	near := config.Config{L3: config.L3Config{Mode: "dial", Carrier: "gre", GREKey: 42,
		LocalIP: "10.5.5.1/30", PeerIP: "10.5.5.2"}}
	far := config.Config{L3: config.L3Config{Mode: "listen", Carrier: "gre", GREKey: 42,
		LocalIP: "10.5.5.2/30", PeerIP: "10.5.5.1"}}
	id, _ := peerRemovalIdentity(Tunnel{Role: "iran"}, near)
	match, side := peerRemovalIdentity(Tunnel{Role: "kharej"}, far)
	if id == "" || match != id || side != "kharej" {
		t.Fatal("GRE endpoints did not share a fingerprint")
	}
	far.L3.GREKey++
	if match, _ := peerRemovalIdentity(Tunnel{Role: "kharej"}, far); match == id {
		t.Fatal("matched another GRE key")
	}

	near = config.Config{Dagger: config.DaggerConfig{Role: "server", License: "shared-license",
		Flavor: "xhttps", IranIP: "192.0.2.1", Port: 2083}}
	far = near
	far.Dagger.Role = "client"
	id, _ = peerRemovalIdentity(Tunnel{Role: "server"}, near)
	match, side = peerRemovalIdentity(Tunnel{Role: "client"}, far)
	if id == "" || match != id || side != "kharej" {
		t.Fatal("Dagger endpoints did not share a fingerprint")
	}
	far.Dagger.IranIP = "192.0.2.2"
	if match, _ := peerRemovalIdentity(Tunnel{Role: "client"}, far); match == id {
		t.Fatal("matched another tunnel using the same license")
	}
}
