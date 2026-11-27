package manage

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/nativebackhaul"
	"github.com/backpack/backpack/internal/pengutunnel"
)

func linkPort(port uint16) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(int(port))
}

func backhaulShare(name string, c config.BackhaulConfig) ShareLink {
	kind, prefix := "reverse", "backhaul-"
	if c.Mode == "tun-ipx" {
		kind = "direct"
	}
	if c.Native {
		prefix = "native-"
	}
	link := ShareLink{
		Name: name, Kind: kind, From: "iran", Tr: prefix + c.Mode,
		Host: c.IranHost, Port: linkPort(c.Port), Tok: c.Token, PeerHost: c.KharejIP,
		LocalIP: c.IranTunnel, PeerIP: c.KharejTunnel, HealthPort: uint32(c.HealthPort), Profile: c.IPXProfile,
		Preset: c.Preset, TunnelLanes: c.Lanes, PacketQueue: c.PacketQueue, MTU: c.MTU,
		HelperHost: c.HTTPHost, HelperPath: c.HTTPPath,
	}
	if c.Mux != nil {
		link.Hosts = c.Mux.Backups
		link.TunnelHeartbeat = c.Mux.HeartbeatSeconds
		link.TunnelTimeout = c.Mux.TimeoutSeconds
	}
	return link
}

func daggerShare(name string, c config.DaggerConfig) ShareLink {
	kind := "reverse"
	if c.Flavor == "tun" {
		kind = "direct"
	}
	return ShareLink{
		Name: name, Kind: kind, From: "iran", Tr: "dagger-" + c.Flavor,
		Host: c.IranIP, Port: linkPort(c.Port), Tok: c.License, PeerHost: c.KharejIP, IranIPv6: c.IranIPv6,
		HelperHost: c.HTTPHost, HelperPath: c.HTTPPath, Profile: c.TUNProfile, TUNPort: uint32(c.TUNPort),
		LocalIP: c.IranTunnelIP, PeerIP: c.KharejTunnelIP,
		TunnelLanes: c.ConnectionPool, TunnelHeartbeat: c.HeartbeatSeconds,
		MTU: c.QuantumMTU, DaggerCipher: c.QuantumCipher, DaggerUpload: c.UploadMode,
	}
}

func importedLink(tr string) bool {
	return strings.HasPrefix(tr, pengutunnel.Prefix) || strings.HasPrefix(tr, "native-") || strings.HasPrefix(tr, "backhaul-") || strings.HasPrefix(tr, "dagger-")
}

func applyImportedLink(l ShareLink, o LinkApplyOptions) (LinkApplied, error) {
	if o.Name != "" {
		l.Name = o.Name
	}
	if err := core.CheckName(l.Name); err != nil {
		return LinkApplied{}, err
	}
	if host := strings.Trim(strings.TrimSpace(o.Host), "[]"); host != "" {
		if l.Tr == "dagger-dc6" && net.ParseIP(host) != nil && net.ParseIP(host).To4() == nil {
			l.IranIPv6 = host
		} else {
			l.Host = host
		}
	}
	var port uint64
	if l.Port != "" {
		var err error
		port, err = strconv.ParseUint(l.Port, 10, 16)
		if err != nil || port == 0 {
			return LinkApplied{}, fmt.Errorf("the setup link has an invalid tunnel port")
		}
	}
	if l.TUNPort > 65535 || l.HealthPort > 65535 {
		return LinkApplied{}, fmt.Errorf("the setup link has an invalid helper port")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	if mode, ok := strings.CutPrefix(l.Tr, pengutunnel.Prefix); ok {
		return applyPenguPeer(ctx, l.Name, penguPeer(l, mode, uint16(port)))
	}
	if mode, ok := strings.CutPrefix(l.Tr, "dagger-"); ok {
		return applyDaggerPeer(ctx, l.Name, config.DaggerConfig{
			Role: "client", Flavor: mode, IranIP: l.Host, IranIPv6: l.IranIPv6, KharejIP: l.PeerHost,
			Port: uint16(port), License: l.Tok, HTTPHost: l.HelperHost, HTTPPath: l.HelperPath,
			TUNProfile: l.Profile, TUNPort: uint16(l.TUNPort), IranTunnelIP: l.LocalIP, KharejTunnelIP: l.PeerIP,
			ConnectionPool: l.TunnelLanes, HeartbeatSeconds: l.TunnelHeartbeat,
			QuantumMTU: l.MTU, QuantumCipher: l.DaggerCipher, UploadMode: l.DaggerUpload,
		})
	}
	mode, native := strings.CutPrefix(l.Tr, "native-")
	mode = strings.TrimPrefix(mode, "backhaul-")
	c := config.BackhaulConfig{
		Native: native, Role: "client", Mode: mode, IranHost: l.Host, KharejIP: l.PeerHost,
		Port: uint16(port), Token: l.Tok, IranTunnel: l.LocalIP, KharejTunnel: l.PeerIP,
		HealthPort: uint16(l.HealthPort), IPXProfile: l.Profile,
		Preset: l.Preset, Lanes: l.TunnelLanes, PacketQueue: l.PacketQueue, MTU: l.MTU,
		HTTPHost: l.HelperHost, HTTPPath: l.HelperPath,
	}
	if native && (len(l.Hosts) != 0 || l.TunnelHeartbeat != 0 || l.TunnelTimeout != 0) {
		backups, err := nativebackhaul.BackupAddresses(l.Hosts, c.IranHost, c.Port)
		if err != nil {
			return LinkApplied{}, err
		}
		c.Mux = &config.BackhaulMuxOptions{Backups: backups, HeartbeatSeconds: l.TunnelHeartbeat, TimeoutSeconds: l.TunnelTimeout}
	}
	return applyBackhaulPeer(ctx, l.Name, c)
}

func applyBackhaulPeer(ctx context.Context, name string, c config.BackhaulConfig) (LinkApplied, error) {
	link := backhaulShare(name, c)
	out := LinkApplied{Name: name, Kind: link.Kind, Transport: c.Mode, StatusUnavailable: !c.Native}
	out.Backups = link.Hosts
	if c.Port != 0 && c.Mode != "tun-ipx" {
		out.Dials = net.JoinHostPort(c.IranHost, link.Port)
	}
	if c.Native {
		if option, ok := nativebackhaul.Find(c.Mode); ok {
			out.Transport = option.Label
		}
	} else if option, ok := backhaulpremium.Find(c.Mode); ok {
		out.Transport = option.Label
	}
	if !c.Native && (c.Mode == "tun-tcp" || c.Mode == "tun-ipx") {
		local, _, _ := strings.Cut(c.KharejTunnel, "/")
		peer, _, _ := strings.Cut(c.IranTunnel, "/")
		out.AwaitPeer = routedPeerProbe(local, peer)
		if c.Mode == "tun-ipx" && out.AwaitPeer != nil {
			out.AwaitPeer = awaitBackhaulIPX(app.ServiceName(name), out.AwaitPeer)
		}
	}
	done, err := installLinkedHelper(out, func(cfg config.Config) bool {
		return cfg.Backhaul.Role == "client" && reflect.DeepEqual(backhaulShare(name, cfg.Backhaul), link)
	}, func() (string, bool, error) { return installBackhaul(ctx, name, c) })
	if err == nil && c.Mode == "tun-ipx" && !done.Active && helperExitedWith(done.Service, backhaulpremium.LicenseExitCode) {
		err = backhaulpremium.ErrLicenseRejected
	}
	return done, err
}

// The engine exits with a non-restarting systemd status if its license fails.
func helperExitedWith(service string, code int) bool {
	if service == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "-p", "ExecMainStatus", "--value", service).Output()
	return err == nil && strings.TrimSpace(string(out)) == strconv.Itoa(code)
}

func awaitBackhaulIPX(service string, probe func(time.Duration) (bool, string)) func(time.Duration) (bool, string) {
	return awaitLicensedTUN(service, probe, backhaulpremium.LicenseExitCode, backhaulpremium.ErrLicenseRejected)
}

func awaitLicensedTUN(service string, probe func(time.Duration) (bool, string), code int, rejected error) func(time.Duration) (bool, string) {
	return func(within time.Duration) (bool, string) {
		end := time.Now().Add(within)
		for {
			if helperExitedWith(service, code) {
				return false, rejected.Error()
			}
			remaining := time.Until(end)
			if remaining <= 0 {
				return false, "no reply across the tunnel"
			}
			if up, detail := probe(min(remaining, 2*time.Second)); up {
				return true, detail
			}
		}
	}
}

func applyDaggerPeer(ctx context.Context, name string, c config.DaggerConfig) (LinkApplied, error) {
	link := daggerShare(name, c)
	out := LinkApplied{Name: name, Kind: link.Kind, Transport: c.Flavor, StatusUnavailable: true}
	if c.Port != 0 {
		host := c.IranIP
		if c.Flavor == "dc6" {
			host = c.IranIPv6
		}
		out.Dials = net.JoinHostPort(host, linkPort(c.Port))
	}
	if c.Flavor == "tun" {
		out.AwaitPeer = routedPeerProbe(c.KharejTunnelIP, c.IranTunnelIP)
	}
	return installLinkedHelper(out, func(cfg config.Config) bool {
		return cfg.Dagger.Role == "client" && reflect.DeepEqual(daggerShare(name, cfg.Dagger), link)
	}, func() (string, bool, error) { return installDagger(ctx, name, c) })
}

func installLinkedHelper(out LinkApplied, matches func(config.Config) bool, install func() (string, bool, error)) (LinkApplied, error) {
	if cfg, err := LoadTunnelConfig(out.Name); err == nil && matches(cfg) {
		service := app.ServiceName(out.Name)
		out.Service, out.Active, out.Existing = service, IsActive(service), true
		if !out.Active {
			if err := core.StartService(service); err != nil {
				return out, err
			}
			out.Active = IsActive(service)
		}
		return out, nil
	}
	var err error
	out.Service, out.Active, err = install()
	return out, err
}
