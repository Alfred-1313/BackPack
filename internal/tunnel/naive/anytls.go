package naive

import (
	"encoding/json"
	"net"
	"os"

	"github.com/backpack/backpack/config"
)

// anyTLSClientJSON wraps only the configured reverse target. The loopback
// SOCKS listener cannot be used to reach other destinations.
func anyTLSClientJSON(c *config.ClientConfig, addr string) ([]byte, error) {
	n := c.Naive
	server, port, err := hostPort(n.Server, false)
	if err != nil {
		return nil, err
	}
	host, localPort, err := hostPort(addr, true)
	if err != nil {
		return nil, err
	}
	target, targetPort, err := hostPort(c.RemoteAddr, true)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(target)
	cidr := ip.String() + "/128"
	if ip.To4() != nil {
		cidr = ip.String() + "/32"
	}
	tlsFields := map[string]any{"enabled": true, "server_name": server}
	if n.CAFile != "" {
		pem, err := os.ReadFile(n.CAFile)
		if err != nil {
			return nil, err
		}
		tlsFields["certificate"] = []string{string(pem)}
	}
	return json.Marshal(map[string]any{
		"log":      map[string]any{"level": "warn"},
		"inbounds": []any{map[string]any{"type": "socks", "listen": host, "listen_port": localPort}},
		"outbounds": []any{map[string]any{"type": "anytls", "tag": "tunnel", "server": server,
			"server_port": port, "password": n.Password, "tls": tlsFields}},
		"route": map[string]any{"rules": []any{
			map[string]any{"ip_cidr": []string{cidr}, "port": []int{targetPort},
				"network": "tcp", "action": "route", "outbound": "tunnel"},
			map[string]any{"action": "reject"},
		}},
	})
}
