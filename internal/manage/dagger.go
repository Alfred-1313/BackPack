package manage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	Name           string `json:"name"`
	Transport      string `json:"transport"`
	IranIP         string `json:"iranIP"`
	KharejIP       string `json:"kharejIP"`
	IranIPv6       string `json:"iranIPv6"`
	Port           uint16 `json:"port"`
	License        string `json:"license"`
	Ports          string `json:"ports"`
	HTTPHost       string `json:"httpHost"`
	HTTPPath       string `json:"httpPath"`
	CertFile       string `json:"certFile"`
	KeyFile        string `json:"keyFile"`
	TUNProfile     string `json:"tunProfile"`
	TUNPort        uint16 `json:"tunPort"`
	IranTunnelIP   string `json:"iranTunnelIP"`
	KharejTunnelIP string `json:"kharejTunnelIP"`
}

type DaggerSetup struct {
	Service string `json:"service"`
	Active  bool   `json:"active"`
	Apply   string `json:"apply"`
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

func DaggerJoinCommand(token string) string {
	return "printf '%s\\n' '" + token + "' | sudo backpack dagger join"
}

func CreateDagger(ctx context.Context, f DaggerForm) (DaggerSetup, error) {
	c := config.DaggerConfig{
		Role: "server", Flavor: f.Transport, IranIP: f.IranIP, KharejIP: f.KharejIP,
		IranIPv6: f.IranIPv6, Port: f.Port, License: strings.TrimSpace(f.License), Ports: f.Ports,
		HTTPHost: f.HTTPHost, HTTPPath: f.HTTPPath, CertFile: f.CertFile, KeyFile: f.KeyFile,
		TUNProfile: f.TUNProfile, TUNPort: f.TUNPort,
		IranTunnelIP: f.IranTunnelIP, KharejTunnelIP: f.KharejTunnelIP,
	}
	service, active, err := installDagger(ctx, f.Name, c)
	if err != nil {
		return DaggerSetup{}, err
	}
	return DaggerSetup{Service: service, Active: active, Apply: DaggerJoinCommand(daggerPeerToken(f.Name, c))}, nil
}

func JoinDagger(ctx context.Context, token string) (string, bool, error) {
	var link daggerLink
	if len(token) > 16<<10 {
		return "", false, fmt.Errorf("DaggerConnect setup token is too long")
	}
	body, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || json.Unmarshal(body, &link) != nil || link.Side.Role != "client" {
		return "", false, fmt.Errorf("invalid DaggerConnect setup token")
	}
	link.Side.Ports, link.Side.CertFile, link.Side.KeyFile = "", "", ""
	return installDagger(ctx, link.Name, link.Side)
}

func installDagger(ctx context.Context, name string, c config.DaggerConfig) (string, bool, error) {
	if err := core.CheckName(name); err != nil {
		return "", false, err
	}
	if _, err := daggerconnect.Build(name, c); err != nil {
		return "", false, err
	}
	if c.Flavor == "https" && c.Role == "server" {
		for _, path := range []string{c.CertFile, c.KeyFile} {
			if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
				return "", false, fmt.Errorf("the HTTPS certificate and private key must exist on Iran")
			}
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
