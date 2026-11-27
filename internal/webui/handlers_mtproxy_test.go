package webui

import (
	"testing"

	"github.com/backpack/backpack/internal/manage"
)

func TestMTProxyUsesOnlyExistingLoopbackMappings(t *testing.T) {
	tunnel := manage.Tunnel{Role: "server", Transport: "wss", Ports: []string{
		"443=8443", "8080", "127.0.0.1:443=9999", "8444=192.0.2.4:8443",
		"6000=6001|6002", "2000-5000", "9000=127.0.0.1:2020",
	}}
	if !mtProxyEligible(tunnel) {
		t.Fatal("reverse WSS tunnel did not offer MTProxy")
	}
	ports := mtProxyPorts(tunnel, "127.0.0.1", "native")
	port, err := chooseMTProxyPort(ports, 443)
	if err != nil || len(ports) != 3 || port.Destination != 8443 || port.listen != "127.0.0.1:443" {
		t.Fatalf("unexpected MTProxy routes: %+v, %v", ports, err)
	}
	if _, err := chooseMTProxyPort(ports, 8444); err == nil {
		t.Fatal("accepted a mapping to an external host")
	}
	web := mtProxyPorts(tunnel, "127.0.0.1", "web")
	if len(web) != 1 || web[0].Destination != 9999 {
		t.Fatalf("WEB must use only the loopback listener: %+v", web)
	}
	tunnel.Transport = "native/dagger-https"
	if !mtProxyEligible(tunnel) {
		t.Fatal("native Dagger HTTPS did not offer MTProxy")
	}
	tunnel.Transport = "udp"
	if mtProxyEligible(tunnel) {
		t.Fatal("accepted a datagram-only tunnel")
	}
}
