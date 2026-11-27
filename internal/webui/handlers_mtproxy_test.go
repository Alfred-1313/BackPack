package webui

import (
	"net"
	"strings"
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

func TestMTProxyPortPlanKeepsExistingServicesAndAcceptsIndependentPorts(t *testing.T) {
	tunnel := manage.Tunnel{Role: "server", Transport: "native/dagger-https", Ports: []string{"2083", "2087"}}
	ports := mtProxyPorts(tunnel, "127.0.0.1", "native")
	if _, _, err := planMTProxyForward(tunnel, ports, 2083, 45100, "127.0.0.1", "native", false); err == nil {
		t.Fatal("replaced a forward that belongs to another service")
	}
	if p, added, err := planMTProxyForward(tunnel, ports, 2083, 2083, "127.0.0.1", "native", false); err != nil || added != "" || p.Destination != 2083 {
		t.Fatalf("existing mapping: %+v %q %v", p, added, err)
	}
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	public := listen.Addr().(*net.TCPAddr).Port
	if _, _, err := planMTProxyForward(tunnel, ports, public, 45100, "127.0.0.1", "native", false); err == nil || !strings.Contains(err.Error(), "in use on this server") {
		t.Fatalf("busy local listener: %v", err)
	}
	listen.Close()
	p, rule, err := planMTProxyForward(tunnel, ports, public, 45100, "127.0.0.1", "native", false)
	if err != nil || !strings.HasSuffix(rule, "=127.0.0.1:45100") || p.Public != public || p.Destination != 45100 {
		t.Fatalf("independent forward: %+v %q %v", p, rule, err)
	}
}
