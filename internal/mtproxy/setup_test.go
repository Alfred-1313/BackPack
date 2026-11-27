package mtproxy

import (
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

func TestFakeTLSSetupMapsTheIngressAndExitSeparately(t *testing.T) {
	s, err := NewSetup(8443, "  WWW.EXAMPLE.COM. ")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(s.Secret)
	if err != nil || raw[0] != 0xee || string(raw[17:]) != "www.example.com" {
		t.Fatal("invalid Fake TLS secret layout")
	}
	link, err := url.Parse(s.Link("2001:db8::1", 443))
	if err != nil {
		t.Fatal(err)
	}
	query := link.Query()
	if link.Host != "t.me" || query.Get("server") != "2001:db8::1" || query.Get("port") != "443" || query.Get("secret") != s.Secret {
		t.Fatal("link does not identify the ingress")
	}
	if !strings.Contains(s.Config(), `bind-to = "127.0.0.1:8443"`) || !strings.Contains(s.Command(), "sudo backpack mtproxy join") {
		t.Fatal("the proxy setup does not bind to the loopback destination")
	}
	if _, err := NewSetup(8443, "https://www.example.com"); err == nil {
		t.Fatal("accepted a URL as the Fake TLS hostname")
	}
}
