package manage

import (
	"fmt"
	"net"
	"strings"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/pengutunnel"
	"github.com/backpack/backpack/internal/tunnel/portmap"
)

type helperBind struct{ network, addr string }

func checkHelperEditPorts(before, after config.Config) error {
	old, err := helperBinds(before)
	if err != nil {
		return err
	}
	next, err := helperBinds(after)
	if err != nil {
		return err
	}
	for bind := range next {
		if !old[bind] && forwardPortHeld(bind.network, bind.addr) {
			return fmt.Errorf("%s (%s) is already in use on this server", bind.addr, strings.ToUpper(bind.network))
		}
	}
	return nil
}

func helperBinds(cfg config.Config) (map[helperBind]bool, error) {
	var control string
	var rules []string
	acceptUDP := false
	binds := make(map[helperBind]bool)
	switch {
	case cfg.Matrix.Present():
		c := cfg.Matrix
		rules, acceptUDP = c.Ports, c.AcceptUDP
		if c.Local.Port != 0 {
			control = ":" + linkPort(c.Local.Port)
			binds[helperBind{"udp", control}] = true
		}
	case cfg.Pengu.Present():
		c := cfg.Pengu
		rules = c.Ports
		if c.Port != 0 && pengutunnel.Listens(c) {
			control = ":" + linkPort(c.Port)
		}
	case cfg.Backhaul.Present():
		c := cfg.Backhaul
		rules = c.Ports
		if c.Role == "server" && c.Port != 0 {
			control = ":" + linkPort(c.Port)
		}
	case cfg.Dagger.Present():
		c := cfg.Dagger
		rules = parsePorts(strings.ReplaceAll(c.Ports, "\n", ","))
		if c.Role == "server" && c.Port != 0 {
			switch c.Flavor {
			case "http", "https", "xhttp", "xhttps", "dc6":
				control = ":" + linkPort(c.Port)
			}
		}
	}
	if control != "" {
		binds[helperBind{"tcp", control}] = true
	}
	type key struct{ network, port string }
	hosts := make(map[key][]string)
	for b := range binds {
		host, port, _ := net.SplitHostPort(b.addr)
		hosts[key{b.network, port}] = []string{host}
	}
	count := 0
	for _, rule := range rules {
		left, _, _ := strings.Cut(rule, "=")
		left = strings.TrimSpace(left)
		tcp, udp := true, acceptUDP
		if strings.HasPrefix(left, "udp:") || strings.HasPrefix(left, "udp/") {
			tcp, udp, left = false, true, left[4:]
		} else if port, ok := strings.CutPrefix(left, "tcp:"); ok {
			left = port
		}
		mappings, err := portmap.Expand([]string{left}, "127.0.0.1")
		if err != nil {
			return nil, err
		}
		count += len(mappings)
		if count > portmap.MaxExpandedPorts {
			return nil, fmt.Errorf("too many forwarded ports")
		}
		for _, m := range mappings {
			host, port, _ := net.SplitHostPort(m.Listen)
			for _, network := range []struct {
				name    string
				enabled bool
			}{{"tcp", tcp}, {"udp", udp}} {
				if !network.enabled {
					continue
				}
				k := key{network.name, port}
				for _, h := range hosts[k] {
					if overlappingForwardBind(host, h) {
						return nil, fmt.Errorf("%s conflicts with another listener in this tunnel", m.Listen)
					}
				}
				hosts[k] = append(hosts[k], host)
				binds[helperBind{network.name, m.Listen}] = true
			}
		}
	}
	return binds, nil
}
