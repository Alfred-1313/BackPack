package manage

import (
	"net"
	"strings"
	"testing"
)

func TestForwardPortCheckDistinguishesListenersAndTargets(t *testing.T) {
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	_, tcpPort, _ := net.SplitHostPort(tcp.Addr().String())
	_, udpPort, _ := net.SplitHostPort(udp.LocalAddr().String())
	got := CheckForwardPorts(ForwardPortCheck{Ports: tcpPort + "=443", TCP: true})
	if !strings.Contains(got.Ports, tcpPort+" (TCP) is already in use on this server") {
		t.Fatalf("busy local port: %+v", got)
	}
	got = CheckForwardPorts(ForwardPortCheck{Ports: "127.0.0.2:" + tcpPort + "=" + tcpPort, TCP: true})
	if got.Ports != "" {
		t.Fatalf("a separate local address and a busy target are allowed: %+v", got)
	}
	got = CheckForwardPorts(ForwardPortCheck{Ports: "udp:" + udpPort, TCP: true})
	if !strings.Contains(got.Ports, udpPort+" (UDP) is already in use on this server") {
		t.Fatalf("busy UDP listener: %+v", got)
	}
	got = CheckForwardPorts(ForwardPortCheck{Proxy: tcp.Addr().String() + "=443", TCP: true})
	if got.Ports != "" || !strings.Contains(got.Proxy, "already in use on this server") {
		t.Fatalf("busy MTProxy listener belongs to the proxy field: %+v", got)
	}
	if err := tcp.Close(); err != nil {
		t.Fatal(err)
	}
	got = CheckForwardPorts(ForwardPortCheck{
		Ports: tcpPort + "=443", TCP: true, Proxy: "127.0.0.1:" + tcpPort + "=8443",
	})
	if got.Ports != "" || !strings.Contains(got.Proxy, "already listed in Forwarded ports") {
		t.Fatalf("MTProxy would collide with a wildcard forward in the form: %+v", got)
	}
	got = CheckForwardPorts(ForwardPortCheck{
		Ports: tcpPort + "=443", TCP: true, TunnelPort: tcpPort, TunnelTransport: "tcp",
	})
	if !strings.Contains(got.Ports, "also used as the tunnel port") {
		t.Fatalf("forward would collide with the new control port: %+v", got)
	}
	got = CheckForwardPorts(ForwardPortCheck{
		Ports: tcpPort + "=443", TCP: true, TunnelPort: tcpPort, TunnelTransport: "quic",
	})
	if got.Ports != "" {
		t.Fatalf("TCP forward may share a UDP tunnel port: %+v", got)
	}
}
