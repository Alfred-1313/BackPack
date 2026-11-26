package manage

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestSetupFamilyKeepsTheIPv6AddressInReverseLinks(t *testing.T) {
	form := NewTunnel{Role: "server", Transport: "tcp", TunnelPort: "[2001:db8::7]:8443", SetupFamily: "ipv6"}
	if err := prepareReverseAddress(&form); err != nil || !form.IPv6 || form.KharejDial != "2001:db8::7" {
		t.Fatalf("IPv6 form lost its bind and dial settings: %+v, %v", form, err)
	}
	form.TunnelPort = "192.0.2.7:8443"
	if err := prepareReverseAddress(&form); err == nil {
		t.Fatal("accepted an IPv4-only bind for an IPv6 tunnel")
	}
}

func TestDaggerTUNSetupDefersUntilKharejAndDoesNotEmbedLicense(t *testing.T) {
	old := linkHost
	linkHost = func() string { return "192.0.2.8" }
	t.Cleanup(func() { linkHost = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := CreateDaggerWithSetup(ctx, ctx, DaggerForm{
		Name: "dagger-pair-fixture", Transport: "tun", License: "fixture-license", SetupFamily: "ipv4",
		TUNProfile: "gre", IranTunnelIP: "10.29.0.1", KharejTunnelIP: "10.29.0.2",
	})
	if err != nil || !out.Setup.Pending || out.Setup.Kind != "direct" || out.Service != "" {
		t.Fatalf("TUN should wait for the actual peer: %+v, %v", out, err)
	}
	link, err := decodePeerJoin(FindSetupLink(out.Apply))
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(link.Addr)
	if err != nil || host != "192.0.2.8" || strings.Contains(out.Apply, "fixture-license") {
		t.Fatalf("unexpected setup address or exposed license: %q (%v)", host, err)
	}
}
