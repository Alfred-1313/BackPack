package manage

import (
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/tunnelspec"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

func (n NewDirectTunnel) isGREPeer() bool {
	return strings.EqualFold(strings.TrimSpace(n.Side), "kharej") && l3.KernelCarrier(n.Carrier)
}

func sameGREEndpoint(a, b string) bool {
	a, b = strings.Trim(strings.TrimSpace(a), "[]"), strings.Trim(strings.TrimSpace(b), "[]")
	if ip := net.ParseIP(a); ip != nil {
		return ip.Equal(net.ParseIP(b))
	}
	return a != "" && strings.EqualFold(a, b)
}

// Reapplying a GRE link can change its random key after Iran is rebuilt.
// Reuse the named listener only for the same remote endpoint. An identical
// key and endpoint also identify an existing listener set up under an alias.
func applyGREPeer(n NewDirectTunnel, allowFreeName bool) (LinkApplied, error) {
	var out LinkApplied
	n.Name = strings.TrimSpace(n.Name)
	if n.Name != "" && !validName(n.Name) {
		return out, fmt.Errorf("invalid GRE tunnel name %q", n.Name)
	}
	if err := os.MkdirAll(app.ConfigDir, 0755); err != nil {
		return out, err
	}
	lock, err := os.OpenFile(app.ConfigDir+"/.gre-peer.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return out, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return out, err
	}

	existing := greTunnelJoining(n.PeerAddr, n.GREKey)
	if n.Name != "" && fileExists(app.ConfigPath(n.Name)) {
		cfg, err := LoadTunnelConfig(n.Name)
		if err != nil {
			return out, err
		}
		if l3.KernelCarrier(cfg.L3.Carrier) && cfg.L3.Mode == "listen" && sameGREEndpoint(cfg.L3.Addr, n.PeerAddr) {
			if existing != "" && existing != n.Name {
				return out, fmt.Errorf("GRE key already belongs to %q", existing)
			}
			existing = n.Name
		} else if allowFreeName {
			n.Name = freeName(n.Name)
		} else {
			return out, fmt.Errorf("name %q belongs to a different tunnel", n.Name)
		}
	}
	if existing == "" {
		if n.Name == "" {
			n.Name = freeName("gre-kharej")
		}
		svc, active, err := createDirectTunnel(n)
		return LinkApplied{Name: n.Name, Service: svc, Kind: "direct", Transport: n.Carrier, Active: active}, err
	}
	path := app.ConfigPath(existing)
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	var cfg config.Config
	md, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return out, err
	}
	old := cfg.L3
	if old.Mode != "listen" || !l3.KernelCarrier(old.Carrier) || !sameGREEndpoint(old.Addr, n.PeerAddr) {
		return out, fmt.Errorf("existing tunnel %q is not the matching GRE listener", existing)
	}
	if n.Name != "" && n.Name != existing && (n.LocalIP != "" && hostOf(n.LocalIP) != hostOf(old.LocalIP) || n.PeerIP != "" && hostOf(n.PeerIP) != hostOf(old.PeerIP)) {
		return out, fmt.Errorf("GRE key belongs to %q with different tunnel addresses", existing)
	}
	n.Name = existing
	if n.LocalIP == "" {
		n.LocalIP = old.LocalIP
	}
	if n.PeerIP == "" {
		n.PeerIP = old.PeerIP
	}
	desired, err := n.greSpecFor(sideKharej, strings.ToLower(strings.TrimSpace(n.Carrier)), existing)
	if err != nil {
		return out, err
	}
	// Keep local interface selection, binding, and administrative settings.
	local := directSpecFrom(existing, old)
	before := local.Render()
	local.Carrier, local.GREKey = desired.Carrier, desired.GREKey
	local.LocalIP, local.PeerIP = desired.LocalIP, desired.PeerIP
	local.MTU, local.MSSClamp = desired.MTU, desired.MSSClamp
	if n.AutoMTU != nil {
		local.AutoMTU = n.AutoMTU
	}
	if n.Preset != "" {
		local.Preset = desired.Preset
	}
	body := local.Render()
	service := app.ServiceName(existing)
	if body != before {
		body, err = carryOver(body, string(raw), md)
		if err != nil {
			return out, err
		}
		if err = validateRendered(body); err != nil {
			return out, err
		}
		if err = tunnelspec.ApplyBody(existing, body, "GRE peer setup"); err != nil {
			return out, err
		}
	} else if !IsActive(service) {
		if err = StartService(service); err != nil {
			return out, err
		}
	}
	return LinkApplied{Name: existing, Service: service, Kind: "direct", Transport: local.Carrier, Active: IsActive(service), Updated: true}, nil
}
