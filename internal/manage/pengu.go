package manage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"strings"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/pengutunnel"
)

type PenguForm struct {
	Name         string `json:"name"`
	Transport    string `json:"transport"`
	Direction    string `json:"direction"`
	Family       string `json:"family"`
	IranHost     string `json:"iranHost"`
	KharejHost   string `json:"kharejHost"`
	IranTunnel   string `json:"iranTunnel"`
	KharejTunnel string `json:"kharejTunnel"`
	Port         uint16 `json:"port"`
	HealthPort   uint16 `json:"healthPort"`
	Secret       string `json:"secret"`
	Ports        string `json:"ports"`
	Preset       string `json:"preset"`
	Cipher       string `json:"cipher"`
	TLSName      string `json:"tlsName"`
	Header       string `json:"header"`
	Pool         int    `json:"pool"`
	Workers      int    `json:"workers"`
	MTU          int    `json:"mtu"`
	Window       int    `json:"window"`
	ICMPType     *int   `json:"icmpType"`
	ICMPCode     int    `json:"icmpCode"`
}

type PenguSetup struct {
	Service string        `json:"service"`
	Active  bool          `json:"active"`
	Setup   SetupLinkInfo `json:"setup"`
}

func penguDefaults(c *config.PenguConfig, mode pengutunnel.Mode) {
	c.Family = orDefault(c.Family, "ipv4")
	c.Preset = orDefault(c.Preset, "turbo")
	c.Cipher = orDefault(c.Cipher, "aes-256-gcm")
	if c.Pool == 0 {
		c.Pool = 2
		if mode.TUN {
			c.Pool = 8
		}
		if mode.IPX {
			c.Pool = 1
		}
	}
	if mode.TUN && c.MTU == 0 {
		c.MTU = 1380
	}
}

func penguFromForm(f PenguForm) (config.PenguConfig, error) {
	mode, ok := pengutunnel.Find(f.Transport)
	if !ok {
		return config.PenguConfig{}, errors.New("unknown PenguTunnel transport")
	}
	c := config.PenguConfig{
		Mode: f.Transport, Direction: f.Direction, Side: "iran", Family: f.Family,
		IranHost: strings.Trim(strings.TrimSpace(f.IranHost), "[]"), KharejHost: strings.Trim(strings.TrimSpace(f.KharejHost), "[]"),
		Port: f.Port, HealthPort: f.HealthPort, Key: strings.TrimSpace(f.Secret),
		IranTunnel: strings.TrimSpace(f.IranTunnel), KharejTunnel: strings.TrimSpace(f.KharejTunnel),
		Preset: f.Preset, Pool: f.Pool, Workers: f.Workers, MTU: f.MTU, Window: f.Window,
		Cipher: f.Cipher, TLSName: strings.TrimSpace(f.TLSName), Header: f.Header,
		ICMPType: f.ICMPType, ICMPCode: f.ICMPCode,
	}
	penguDefaults(&c, mode)
	if value, err := setupTuningValue(pengutunnel.Prefix+c.Mode, SetupTuning{Preset: c.Preset}); err == nil {
		if mode.IPX && f.MTU == 0 {
			c.MTU = value
		} else if !mode.IPX && f.Pool == 0 {
			c.Pool = value
		}
	}
	if err := validateSetupFamily(c.Family, dualSetupFamilies); err != nil {
		return c, err
	}
	if c.IranHost == "" {
		var err error
		c.IranHost, err = setupPublicAddress(c.Family)
		if err != nil {
			return c, err
		}
	}
	if c.Key == "" {
		var err error
		c.Key, err = backhaulToken()
		if err != nil {
			return c, err
		}
	}
	for _, rule := range strings.FieldsFunc(f.Ports, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if rule = strings.TrimSpace(rule); rule != "" {
			if p, ok := strings.CutPrefix(rule, "udp:"); ok {
				rule = "udp/" + p
			}
			c.Ports = append(c.Ports, strings.TrimPrefix(rule, "tcp:"))
		}
	}
	if c.HealthPort == 0 {
		c.HealthPort = uint16(ctPickPort(map[int]bool{int(c.Port): true}, false))
	}
	if mode.TUN && c.IranTunnel == "" && c.KharejTunnel == "" {
		pair := config.BackhaulConfig{Token: c.Key}
		if err := allocateBackhaulTUN(f.Name, &pair); err != nil {
			return c, err
		}
		c.IranTunnel, c.KharejTunnel = pair.IranTunnel, pair.KharejTunnel
	}
	return c, pengutunnel.Validate(c)
}

func CreatePengu(ctx context.Context, f PenguForm) (PenguSetup, error) {
	if err := core.CheckName(f.Name); err != nil {
		return PenguSetup{}, err
	}
	cfg, err := penguFromForm(f)
	if err != nil {
		return PenguSetup{}, err
	}
	setup, err := importedSetupLink(penguShare(f.Name, cfg))
	if err != nil {
		return PenguSetup{}, err
	}
	service, active, err := installPengu(ctx, f.Name, cfg)
	return PenguSetup{Service: service, Active: active, Setup: setup}, err
}

func checkPenguLocal(c config.PenguConfig) error {
	if err := pengutunnel.Validate(c); err != nil {
		return err
	}
	mode, _ := pengutunnel.Find(c.Mode)
	host, subnet := c.IranHost, c.IranTunnel
	if c.Side == "kharej" {
		host, subnet = c.KharejHost, c.KharejTunnel
	}
	if mode.IPX {
		if _, err := pengutunnel.LocalInterface(host); err != nil {
			return err
		}
	}
	if mode.TUN {
		if err := backhaulTUNAvailable(subnet); err != nil {
			return fmt.Errorf("PenguTunnel: %w", err)
		}
	}
	return nil
}

func installPengu(ctx context.Context, name string, c config.PenguConfig) (string, bool, error) {
	if err := checkPenguLocal(c); err != nil {
		return "", false, err
	}
	return installHelperTunnel(ctx, name, struct {
		Pengu config.PenguConfig `toml:"pengu"`
	}{c}, pengutunnel.Ensure)
}

func penguShare(name string, c config.PenguConfig) ShareLink {
	l := ShareLink{Name: name, Kind: c.Direction, From: "iran", Tr: pengutunnel.Prefix + c.Mode,
		Host: c.IranHost, PeerHost: c.KharejHost, Tok: c.Key, Port: linkPort(c.Port),
		LocalIP: c.IranTunnel, PeerIP: c.KharejTunnel, HealthPort: uint32(c.HealthPort),
		Preset: c.Preset, TunnelLanes: c.Pool, MTU: c.MTU, HelperSNI: c.TLSName,
		PenguFamily: c.Family, PenguCipher: c.Cipher, PenguHeader: c.Header, PenguWorkers: c.Workers,
		PenguWindow: c.Window, PenguICMPCode: c.ICMPCode,
	}
	if c.ICMPType != nil {
		l.PenguICMPType = *c.ICMPType + 1
	}
	return l
}

func penguPeer(l ShareLink, mode string, port uint16) config.PenguConfig {
	c := config.PenguConfig{Side: "kharej", Mode: mode, Direction: l.Kind, Family: l.PenguFamily,
		IranHost: l.Host, KharejHost: l.PeerHost, Key: l.Tok, Port: port,
		IranTunnel: l.LocalIP, KharejTunnel: l.PeerIP, HealthPort: uint16(l.HealthPort),
		Preset: l.Preset, Pool: l.TunnelLanes, MTU: l.MTU, TLSName: l.HelperSNI,
		Cipher: l.PenguCipher, Header: l.PenguHeader, Workers: l.PenguWorkers,
		Window: l.PenguWindow, ICMPCode: l.PenguICMPCode,
	}
	if l.PenguICMPType != 0 {
		v := l.PenguICMPType - 1
		c.ICMPType = &v
	}
	return c
}

func applyPenguPeer(ctx context.Context, name string, c config.PenguConfig) (LinkApplied, error) {
	if err := pengutunnel.Validate(c); err != nil {
		return LinkApplied{}, err
	}
	mode, _ := pengutunnel.Find(c.Mode)
	out := LinkApplied{Name: name, Kind: c.Direction, Transport: mode.Label, StatusUnavailable: true}
	if !mode.IPX && !pengutunnel.Listens(c) {
		out.Dials = net.JoinHostPort(c.IranHost, strconv.Itoa(int(c.Port)))
	}
	if mode.TUN {
		local, _ := netip.ParsePrefix(c.KharejTunnel)
		peer, _ := netip.ParsePrefix(c.IranTunnel)
		out.AwaitPeer = awaitLicensedTUN(app.ServiceName(name),
			routedPeerProbe(local.Addr().String(), peer.Addr().String()), pengutunnel.LicenseExitCode, pengutunnel.ErrLicenseRejected)
	}
	done, err := installLinkedHelper(out, func(cfg config.Config) bool {
		return reflect.DeepEqual(cfg.Pengu, c)
	}, func() (string, bool, error) { return installPengu(ctx, name, c) })
	if err == nil && !done.Active && helperExitedWith(done.Service, pengutunnel.LicenseExitCode) {
		err = pengutunnel.ErrLicenseRejected
	}
	return done, err
}
