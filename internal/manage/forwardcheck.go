package manage

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"

	"github.com/backpack/backpack/internal/tunnel/portmap"
)

type ForwardPortCheck struct {
	Ports           string `json:"ports"`
	Proxy           string `json:"proxy"`
	TCP             bool   `json:"tcp"`
	UDP             bool   `json:"udp"`
	TunnelPort      string `json:"tunnelPort"`
	TunnelTransport string `json:"tunnelTransport"`
}

type ForwardPortConflicts struct {
	Ports string `json:"ports"`
	Proxy string `json:"proxy"`
}

// CheckForwardPorts only probes listeners on the local server. The target
// side of each rule is validated by the carrier's setup handler.
func CheckForwardPorts(c ForwardPortCheck) ForwardPortConflicts {
	controlHost, controlPort, _ := net.SplitHostPort(c.TunnelPort)
	if validPort(c.TunnelPort) {
		controlPort = c.TunnelPort
	}
	controlNetwork := "tcp"
	if isDatagram(c.TunnelTransport) {
		controlNetwork = "udp"
	}
	type socket struct{ network, port string }
	seen := map[socket][]string{}
	var result ForwardPortConflicts
	count := 0
	for _, field := range []struct {
		text  string
		proxy bool
		msg   *string
	}{{c.Ports, false, &result.Ports}, {c.Proxy, true, &result.Proxy}} {
		for _, rule := range parsePorts(strings.ReplaceAll(field.text, "\n", ",")) {
			left, _, _ := strings.Cut(rule, "=")
			left = strings.TrimSpace(left)
			tcp, udp := c.TCP || field.proxy, c.UDP
			switch {
			case strings.HasPrefix(left, "udp:") || strings.HasPrefix(left, "udp/"):
				tcp, udp, left = false, true, left[4:]
			case strings.HasPrefix(left, "tcp:"):
				tcp, udp, left = true, false, left[4:]
			}
			maps, err := portmap.Expand([]string{left}, "127.0.0.1")
			if err != nil {
				continue
			}
			count += len(maps)
			if count > portmap.MaxExpandedPorts {
				return result
			}
			for _, m := range maps {
				host, port, _ := net.SplitHostPort(m.Listen)
				if host != "" {
					ip, err := netip.ParseAddr(host)
					if err != nil {
						continue
					}
					host = ip.Unmap().String()
				}
				for _, protocol := range []struct {
					name    string
					enabled bool
				}{{"tcp", tcp}, {"udp", udp}} {
					if !protocol.enabled {
						continue
					}
					key := socket{protocol.name, port}
					if *field.msg == "" {
						switch {
						case port == controlPort && protocol.name == controlNetwork && overlappingForwardBind(host, controlHost):
							*field.msg = fmt.Sprintf("Port %s (%s) is also used as the tunnel port.", port, strings.ToUpper(protocol.name))
						case forwardPortHeld(protocol.name, m.Listen):
							*field.msg = fmt.Sprintf("Port %s (%s) is already in use on this server.", port, strings.ToUpper(protocol.name))
						default:
							for _, other := range seen[key] {
								if overlappingForwardBind(host, other) {
									*field.msg = fmt.Sprintf("Port %s (%s) is already listed in Forwarded ports.", port, strings.ToUpper(protocol.name))
									break
								}
							}
						}
					}
					seen[key] = append(seen[key], host)
				}
			}
		}
	}
	return result
}

func overlappingForwardBind(a, b string) bool {
	wildcard := func(host string) bool { return host == "" || host == "0.0.0.0" || host == "::" }
	return a == b || wildcard(a) || wildcard(b)
}

func forwardPortHeld(network, addr string) bool {
	if network == "tcp" {
		return portHeld(addr)
	}
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return errors.Is(err, syscall.EADDRINUSE)
	}
	conn.Close()
	return false
}
