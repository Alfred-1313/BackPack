package manage

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/backpack/backpack/config"
)

func TestDaggerPeerTokenUsesTheOtherSide(t *testing.T) {
	token := daggerPeerToken("linked", config.DaggerConfig{
		Role: "server", Flavor: "https", IranIP: "192.0.2.1", Port: 43101, License: "test-psk",
		Ports: "44101", CertFile: "/private/cert.pem", KeyFile: "/private/key.pem",
	})
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	var peer daggerLink
	if err := json.Unmarshal(raw, &peer); err != nil {
		t.Fatal(err)
	}
	if peer.Name != "linked" || peer.Side.Role != "client" || peer.Side.Flavor != "https" ||
		peer.Side.IranIP != "192.0.2.1" || peer.Side.Port != 43101 || peer.Side.License != "test-psk" ||
		peer.Side.Ports != "" || peer.Side.CertFile != "" || peer.Side.KeyFile != "" {
		t.Fatal("the peer handoff changed the shared settings or included server-only settings")
	}
}
