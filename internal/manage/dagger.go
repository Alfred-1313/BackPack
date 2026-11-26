package manage

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/daggerconnect"
	"github.com/backpack/backpack/internal/manage/core"
)

type DaggerForm struct {
	Name           string      `json:"name"`
	Transport      string      `json:"transport"`
	IranIP         string      `json:"iranIP"`
	KharejIP       string      `json:"kharejIP"`
	IranIPv6       string      `json:"iranIPv6"`
	Port           uint16      `json:"port"`
	License        string      `json:"license"`
	Ports          string      `json:"ports"`
	HTTPHost       string      `json:"httpHost"`
	HTTPPath       string      `json:"httpPath"`
	CertFile       string      `json:"certFile"`
	KeyFile        string      `json:"keyFile"`
	TUNProfile     string      `json:"tunProfile"`
	TUNPort        uint16      `json:"tunPort"`
	IranTunnelIP   string      `json:"iranTunnelIP"`
	KharejTunnelIP string      `json:"kharejTunnelIP"`
	QuantumProfile string      `json:"quantumProfile"`
	QuantumMTU     int         `json:"quantumMTU"`
	QuantumCipher  string      `json:"quantumCipher"`
	UploadMode     string      `json:"uploadMode"`
	Tuning         SetupTuning `json:"tuning"`
	SetupFamily    string      `json:"setupFamily"`
}

type DaggerSetup struct {
	Service string        `json:"service"`
	Active  bool          `json:"active"`
	Apply   string        `json:"apply"`
	Setup   SetupLinkInfo `json:"setup"`
}

type daggerLink struct {
	Name string
	Side config.DaggerConfig
}

func daggerPeerToken(name string, c config.DaggerConfig) string {
	c.Role, c.Ports, c.CertFile, c.KeyFile = "client", "", "", ""
	body, _ := json.Marshal(daggerLink{Name: name, Side: c})
	return base64.RawURLEncoding.EncodeToString(body)
}

func CreateDagger(ctx context.Context, f DaggerForm) (DaggerSetup, error) {
	return CreateDaggerWithSetup(ctx, ctx, f)
}

func CreateDaggerWithSetup(ctx, lifetime context.Context, f DaggerForm) (DaggerSetup, error) {
	f.IranIP, f.IranIPv6 = strings.Trim(strings.TrimSpace(f.IranIP), "[]"), strings.Trim(strings.TrimSpace(f.IranIPv6), "[]")
	if err := validateSetupFamily(f.SetupFamily, DaggerSetupFamilies(f.Transport)); err != nil {
		return DaggerSetup{}, err
	}
	if f.IranIP == "" {
		f.IranIP = linkHost()
	}
	if f.Transport == "dc6" && f.IranIPv6 == "" {
		addr, err := setupPublicAddress("ipv6")
		if err != nil {
			return DaggerSetup{}, err
		}
		f.IranIPv6 = addr
	}
	if f.Transport == "tun" && f.Port == 0 {
		f.Port = 8443
	}
	if f.Transport == "xhttps" && f.HTTPPath == "" {
		f.HTTPPath = "/api/v2"
	}
	c := config.DaggerConfig{
		Role: "server", Flavor: f.Transport, IranIP: f.IranIP, KharejIP: f.KharejIP,
		IranIPv6: f.IranIPv6, Port: f.Port, License: strings.TrimSpace(f.License), Ports: f.Ports,
		HTTPHost: f.HTTPHost, HTTPPath: f.HTTPPath, CertFile: f.CertFile, KeyFile: f.KeyFile,
		TUNProfile: f.TUNProfile, TUNPort: f.TUNPort,
		IranTunnelIP: f.IranTunnelIP, KharejTunnelIP: f.KharejTunnelIP,
		QuantumMTU: f.QuantumMTU, QuantumCipher: f.QuantumCipher, UploadMode: f.UploadMode,
	}
	if f.QuantumProfile != "" {
		if f.Transport != "quantum" || f.QuantumProfile != "gaming" {
			return DaggerSetup{}, fmt.Errorf("Gaming is a Quantum profile")
		}
		c.Flavor = "quantum-gaming"
	}
	if f.Tuning != (SetupTuning{}) {
		value, err := setupTuningValue("dagger-"+f.Transport, f.Tuning)
		if err != nil {
			return DaggerSetup{}, err
		}
		if f.Transport == "tun" {
			c.HeartbeatSeconds = value
		} else {
			c.ConnectionPool = value
		}
	}
	if c.Flavor == "tun" && c.KharejIP == "" {
		if err := core.CheckName(f.Name); err != nil {
			return DaggerSetup{}, err
		}
		if net.ParseIP(c.IranIP).To4() == nil {
			return DaggerSetup{}, fmt.Errorf("could not detect the public IPv4 address on Iran")
		}
		preview := c
		preview.KharejIP = "192.0.2.1"
		if _, err := daggerconnect.Build(f.Name, preview); err != nil {
			return DaggerSetup{}, err
		}
		setup, err := beginPeerJoinAt(lifetime, c.IranIP, f.Name, "direct", func(ctx context.Context, _, peer string) (peerJoinResult, error) {
			paired := c
			paired.KharejIP = peer
			link, err := daggerShare(f.Name, paired).Encode()
			if err != nil {
				return peerJoinResult{}, err
			}
			_, _, err = installDagger(ctx, f.Name, paired)
			return peerJoinResult{Link: link}, err
		})
		return DaggerSetup{Apply: setup.Apply, Setup: setup}, err
	}
	setup, err := importedSetupLink(daggerShare(f.Name, c))
	if err != nil {
		return DaggerSetup{}, err
	}
	service, active, err := installDagger(ctx, f.Name, c)
	if err != nil {
		return DaggerSetup{}, err
	}
	return DaggerSetup{Service: service, Active: active, Apply: setup.Apply, Setup: setup}, nil
}

func JoinDagger(ctx context.Context, token string) (LinkApplied, error) {
	var link daggerLink
	if len(token) > 16<<10 {
		return LinkApplied{}, fmt.Errorf("DaggerConnect setup token is too long")
	}
	body, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || json.Unmarshal(body, &link) != nil || link.Side.Role != "client" {
		return LinkApplied{}, fmt.Errorf("invalid DaggerConnect setup token")
	}
	link.Side.Ports, link.Side.CertFile, link.Side.KeyFile = "", "", ""
	if err := core.CheckName(link.Name); err != nil {
		return LinkApplied{}, err
	}
	return applyDaggerPeer(ctx, link.Name, link.Side)
}

func installDagger(ctx context.Context, name string, c config.DaggerConfig) (string, bool, error) {
	if err := core.CheckName(name); err != nil {
		return "", false, err
	}
	if _, err := daggerconnect.Build(name, c); err != nil {
		return "", false, err
	}
	if daggerconnect.UsesTLSFiles(c.Flavor) && c.Role == "server" {
		for _, path := range []string{c.CertFile, c.KeyFile} {
			if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
				return "", false, fmt.Errorf("the TLS certificate and private key must be regular files on Iran")
			}
		}
		if _, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile); err != nil {
			return "", false, fmt.Errorf("the TLS certificate and private key must be a matching PEM pair")
		}
	}
	return installHelperTunnel(ctx, name, struct {
		Dagger config.DaggerConfig `toml:"dagger"`
	}{Dagger: c}, daggerconnect.Ensure)
}

func installHelperTunnel(ctx context.Context, name string, cfg any, ensure func(context.Context) error) (string, bool, error) {
	service := app.ServiceName(name)
	unit := filepath.Join(app.ServiceDir, service)
	if _, err := os.Lstat(unit); !os.IsNotExist(err) {
		return "", false, fmt.Errorf("a service named %q already exists", service)
	}
	if _, err := os.Lstat(app.ConfigPath(name)); !os.IsNotExist(err) {
		return "", false, fmt.Errorf("a tunnel named %q already exists", name)
	}
	if ensure != nil {
		if err := ensure(ctx); err != nil {
			return "", false, err
		}
	}
	if err := os.MkdirAll(app.ConfigDir, 0755); err != nil {
		return "", false, err
	}
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(cfg); err != nil {
		return "", false, err
	}
	file, err := os.OpenFile(app.ConfigPath(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, app.TunnelConfigMode)
	if err != nil {
		return "", false, err
	}
	configured := false
	defer func() {
		if !configured {
			_ = os.Remove(app.ConfigPath(name))
			_ = os.Remove(unit)
		}
	}()
	_, err = file.Write(body.Bytes())
	closeErr := file.Close()
	if err != nil {
		return "", false, err
	}
	if closeErr != nil {
		return "", false, closeErr
	}
	if err := core.WriteUnit(name); err != nil {
		return "", false, err
	}
	if err := core.DaemonReload(); err != nil {
		return "", false, err
	}
	configured = true // retain the service and its log if the launch fails
	if err := core.StartService(service); err != nil {
		return service, false, err
	}
	return service, core.IsActive(service), nil
}
