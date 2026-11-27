package manage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/nativebackhaul"
)

type BackhaulForm struct {
	Name         string           `json:"name"`
	Mode         string           `json:"transport"`
	IranHost     string           `json:"iranHost"`
	KharejIP     string           `json:"kharejIP"`
	Port         uint16           `json:"port"`
	Secret       string           `json:"secret"`
	Ports        string           `json:"ports"`
	IranTunnel   string           `json:"iranTunnel"`
	KharejTunnel string           `json:"kharejTunnel"`
	IPXProfile   string           `json:"ipxProfile"`
	HealthPort   uint16           `json:"healthPort"`
	Tuning       SetupTuning      `json:"tuning"`
	Mux          *BackhaulMuxForm `json:"mux"`
	HTTPPath     string           `json:"httpPath"`
	HTTPHost     string           `json:"httpHost"`
	SetupFamily  string           `json:"setupFamily"`
}

type BackhaulMuxForm struct {
	KharejDial       string       `json:"kharejDial"`
	KharejBackups    string       `json:"kharejBackups"`
	HeartbeatSeconds int          `json:"heartbeatSeconds"`
	TimeoutSeconds   int          `json:"timeoutSeconds"`
	IPv6             bool         `json:"ipv6"`
	ProxyProtocol    bool         `json:"proxyProtocol"`
	Limits           TunnelLimits `json:"limits"`
}

type BackhaulSetup struct {
	Service string        `json:"service"`
	Active  bool          `json:"active"`
	Apply   string        `json:"apply"`
	Setup   SetupLinkInfo `json:"setup"`
}

type backhaulLink struct {
	Name string
	Side config.BackhaulConfig
}

func backhaulToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf[:]), nil
}

func BackhaulOptions() []backhaulpremium.Option {
	ipx, _ := backhaulpremium.Find("tun-ipx")
	return append(nativebackhaul.Options(), ipx)
}

func CreateBackhaul(ctx context.Context, f BackhaulForm) (BackhaulSetup, error) {
	value, err := setupTuningValue("native-"+f.Mode, f.Tuning)
	if err != nil {
		return BackhaulSetup{}, err
	}
	host := strings.Trim(strings.TrimSpace(f.IranHost), "[]")
	if f.Mux != nil {
		if !nativebackhaul.HasMuxOptions(f.Mode) {
			return BackhaulSetup{}, errors.New("these options apply to XTcpMux and XWsMux")
		}
		if dial := strings.Trim(strings.TrimSpace(f.Mux.KharejDial), "[]"); dial != "" {
			host = dial
		}
	}
	if f.SetupFamily != "" {
		if err := validateSetupFamily(f.SetupFamily, dualSetupFamilies); err != nil {
			return BackhaulSetup{}, err
		}
		if host == "" {
			host, err = setupPublicAddress(f.SetupFamily)
			if err != nil {
				return BackhaulSetup{}, err
			}
		}
	}
	if f.SetupFamily == "ipv6" && net.ParseIP(host).To4() != nil {
		return BackhaulSetup{}, errors.New("the Kharej dial address must be IPv6 or a DNS name for the IPv6 setup")
	}
	if host == "" {
		host = linkHost()
	}
	if host == "" {
		if ip := PublicIPv6(); ip != "-" {
			host = ip
		}
	}
	if host == "" {
		return BackhaulSetup{}, errors.New("could not detect the Iran address; enter an Iran IP or hostname")
	}
	token, err := backhaulFormToken(f.Secret)
	if err != nil {
		return BackhaulSetup{}, err
	}
	c := config.BackhaulConfig{
		Native: true, Role: "server", Mode: f.Mode, Port: f.Port, Token: token,
		IranHost: host, KharejIP: strings.TrimSpace(f.KharejIP),
		IranTunnel: strings.TrimSpace(f.IranTunnel), KharejTunnel: strings.TrimSpace(f.KharejTunnel),
		HTTPHost: strings.TrimSpace(f.HTTPHost), HTTPPath: strings.TrimSpace(f.HTTPPath),
		IPXProfile: f.IPXProfile, Ports: strings.FieldsFunc(f.Ports, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
		}),
	}
	if f.Mux != nil {
		if f.SetupFamily == "ipv6" {
			f.Mux.IPv6 = true
		}
		backups, err := nativebackhaul.BackupAddresses(strings.Fields(strings.ReplaceAll(f.Mux.KharejBackups, ",", " ")), host, f.Port)
		if err != nil {
			return BackhaulSetup{}, err
		}
		c.Mux = &config.BackhaulMuxOptions{
			Backups: backups, HeartbeatSeconds: f.Mux.HeartbeatSeconds, TimeoutSeconds: f.Mux.TimeoutSeconds,
			IPv6: &f.Mux.IPv6, ProxyProtocol: f.Mux.ProxyProtocol,
			MaxConnections: f.Mux.Limits.MaxConnections, BandwidthMbps: f.Mux.Limits.BandwidthMbps,
		}
	}
	c.Preset = strings.ToLower(strings.TrimSpace(f.Tuning.Preset))
	if c.Mode == "tun-tcp" {
		c.PacketQueue = value
		if f.Tuning.MTU != nil {
			c.MTU = *f.Tuning.MTU
		}
	} else {
		c.Lanes = value
	}
	if c.Mode == "tun-tcp" {
		if err := allocateBackhaulTUN(f.Name, &c); err != nil {
			return BackhaulSetup{}, err
		}
	}
	setup, err := importedSetupLink(backhaulShare(f.Name, c))
	if err != nil {
		return BackhaulSetup{}, err
	}
	service, active, err := installBackhaul(ctx, f.Name, c)
	if err != nil {
		return BackhaulSetup{}, err
	}
	return BackhaulSetup{Service: service, Active: active, Apply: setup.Apply, Setup: setup}, nil
}

func allocateBackhaulTUN(name string, c *config.BackhaulConfig) error {
	if c.IranTunnel != "" || c.KharejTunnel != "" {
		return nil // validate the explicit pair with the remainder of the config
	}
	for i := range 16 {
		near, far := ctBackhaulPair(name + c.Token + fmt.Sprint(i))
		if err := backhaulTUNAvailable(near); err == nil {
			c.IranTunnel, c.KharejTunnel = near, far
			return nil
		}
	}
	return errors.New("no free Backhaul TUN subnet was found")
}

const defaultBackhaulIPXProfile = "icmp"

func backhaulIPXConfig(f BackhaulForm, iran, kharej string) (config.BackhaulConfig, error) {
	if f.Port != 0 {
		return config.BackhaulConfig{}, errors.New("IPX uses IPv4 endpoints without a tunnel listener port")
	}
	mtu, err := setupTuningValue("backhaul-tun-ipx", f.Tuning)
	if err != nil {
		return config.BackhaulConfig{}, err
	}
	token, err := backhaulFormToken(f.Secret)
	if err != nil {
		return config.BackhaulConfig{}, err
	}
	c := config.BackhaulConfig{
		Role: "server", Mode: "tun-ipx", IranHost: iran, KharejIP: kharej,
		HealthPort: f.HealthPort, Token: token, MTU: mtu,
		IPXProfile: orDefault(strings.ToLower(strings.TrimSpace(f.IPXProfile)), defaultBackhaulIPXProfile),
		Preset:     strings.ToLower(strings.TrimSpace(f.Tuning.Preset)),
		IranTunnel: strings.TrimSpace(f.IranTunnel), KharejTunnel: strings.TrimSpace(f.KharejTunnel),
		Ports: strings.FieldsFunc(f.Ports, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' }),
	}
	if c.HealthPort == 0 {
		c.HealthPort = uint16(ctPickPort(make(map[int]bool), true))
	}
	if err := allocateBackhaulTUN(f.Name, &c); err != nil {
		return c, err
	}
	return c, backhaulpremium.Validate(c)
}

// StartBackhaulIPX defers the raw IPv4 tunnel until Kharej authenticates and
// supplies its endpoint. Both sides keep using the official IPX engine.
func StartBackhaulIPX(request, lifetime context.Context, f BackhaulForm) (BackhaulSetup, error) {
	f.Name = strings.TrimSpace(f.Name)
	if err := core.CheckName(f.Name); err != nil {
		return BackhaulSetup{}, err
	}
	if fileExists(app.ConfigPath(f.Name)) {
		return BackhaulSetup{}, errors.New("a tunnel with this name already exists")
	}
	if f.SetupFamily != "" && f.SetupFamily != "ipv4" {
		return BackhaulSetup{}, errors.New("Backhaul IPX requires IPv4 on both endpoints")
	}
	host := strings.TrimSpace(f.IranHost)
	if host == "" {
		var err error
		host, err = setupPublicAddress("ipv4")
		if err != nil {
			return BackhaulSetup{}, err
		}
	}
	peer := strings.TrimSpace(f.KharejIP)
	preview := peer
	if preview == "" {
		preview = "192.0.2.1"
		if preview == host {
			preview = "192.0.2.2"
		}
	}
	c, err := backhaulIPXConfig(f, host, preview)
	if err != nil {
		return BackhaulSetup{}, err
	}
	if _, err := backhaulpremium.LocalInterface(host); err != nil {
		return BackhaulSetup{}, err
	}
	check, cancel := context.WithTimeout(request, 30*time.Second)
	defer cancel()
	if err := backhaulpremium.Ensure(check); err != nil {
		return BackhaulSetup{}, err
	}
	info, err := beginPeerJoinAt(lifetime, host, f.Name, "direct", func(ctx context.Context, _, addr string) (peerJoinResult, error) {
		paired := c
		paired.KharejIP = peer
		if paired.KharejIP == "" {
			paired.KharejIP = addr
		}
		if ip, err := netip.ParseAddr(paired.KharejIP); err != nil || !ip.Is4() {
			return peerJoinResult{}, errors.New("Backhaul IPX could not detect a Kharej IPv4 endpoint")
		}
		setup, err := importedSetupLink(backhaulShare(f.Name, paired))
		if err != nil {
			return peerJoinResult{}, err
		}
		_, _, err = installBackhaul(ctx, f.Name, paired)
		return peerJoinResult{Link: setup.Link}, err
	})
	return BackhaulSetup{Apply: info.Apply, Setup: info}, err
}

func backhaulFormToken(secret string) (string, error) {
	if secret == "" {
		return backhaulToken()
	}
	if err := validateRoutedSecret(secret); err != nil {
		return "", err
	}
	if decoded, err := base64.StdEncoding.DecodeString(secret); err == nil && len(decoded) == 32 {
		return secret, nil
	}
	key, err := hex.DecodeString(routedKey(secret))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func backhaulPeerToken(name string, c config.BackhaulConfig) string {
	c.Role, c.Ports = "client", nil
	if c.Mux != nil {
		c.Mux = &config.BackhaulMuxOptions{Backups: c.Mux.Backups, HeartbeatSeconds: c.Mux.HeartbeatSeconds, TimeoutSeconds: c.Mux.TimeoutSeconds}
	}
	body, _ := json.Marshal(backhaulLink{Name: name, Side: c})
	return base64.RawURLEncoding.EncodeToString(body)
}

func JoinBackhaul(ctx context.Context, token string) (LinkApplied, error) {
	var link backhaulLink
	if len(token) > 16<<10 {
		return LinkApplied{}, errors.New("Backhaul setup token is too long")
	}
	body, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || json.Unmarshal(body, &link) != nil || link.Side.Role != "client" || len(link.Side.Ports) != 0 {
		return LinkApplied{}, errors.New("invalid Backhaul setup token")
	}
	if err := core.CheckName(link.Name); err != nil {
		return LinkApplied{}, err
	}
	return applyBackhaulPeer(ctx, link.Name, link.Side)
}

func installBackhaul(ctx context.Context, name string, c config.BackhaulConfig) (string, bool, error) {
	if err := core.CheckName(name); err != nil {
		return "", false, err
	}
	validate := backhaulpremium.Validate
	ensure := backhaulpremium.Ensure
	if c.Native {
		validate, ensure = nativebackhaul.Validate, nil
	}
	if err := validate(c); err != nil {
		return "", false, err
	}
	if c.Mode == "tun-ipx" {
		address := c.IranHost
		if c.Role == "client" {
			address = c.KharejIP
		}
		if _, err := backhaulpremium.LocalInterface(address); err != nil {
			return "", false, err
		}
	}
	if backhaulpremium.IsTUN(c.Mode) {
		local := c.IranTunnel
		if c.Role == "client" {
			local = c.KharejTunnel
		}
		if err := backhaulTUNAvailable(local); err != nil {
			return "", false, err
		}
	}
	return installHelperTunnel(ctx, name, struct {
		Backhaul config.BackhaulConfig `toml:"backhaul"`
	}{Backhaul: c}, ensure)
}
