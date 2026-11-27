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

func TestWEBSetupMatchesTelemtRootCapability(t *testing.T) {
	s := Setup{Mode: ModeWeb, Port: 8443, WebHost: "proxy.example.com", WebIP: "203.0.113.10",
		Secret: "dd000102030405060708090a0b0c0d0e0f"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := s.webCapability(); got != "IpJrt3e7sKtzPyoXy6w-Zj6GGEvsvclN66JzQEfPYLA" {
		t.Fatalf("WEB capability = %s", got)
	}
	if !strings.HasPrefix(s.Link("", 0), "tg://webproxy?") || !strings.Contains(s.Config(), `transport = "web"`) {
		t.Fatal("did not produce the WEB link and listener")
	}
}
