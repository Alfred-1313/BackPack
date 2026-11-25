package manage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/manage/core"
)

type BackhaulForm struct {
	Name         string `json:"name"`
	Mode         string `json:"transport"`
	IranHost     string `json:"iranHost"`
	KharejIP     string `json:"kharejIP"`
	Port         uint16 `json:"port"`
	Ports        string `json:"ports"`
	IranTunnel   string `json:"iranTunnel"`
	KharejTunnel string `json:"kharejTunnel"`
	IPXProfile   string `json:"ipxProfile"`
}

type BackhaulSetup struct {
	Service string `json:"service"`
	Active  bool   `json:"active"`
	Apply   string `json:"apply"`
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

func CreateBackhaul(ctx context.Context, f BackhaulForm) (BackhaulSetup, error) {
	token, err := backhaulToken()
	if err != nil {
		return BackhaulSetup{}, err
	}
	c := config.BackhaulConfig{
		Role: "server", Mode: f.Mode, Port: f.Port, Token: token,
		IranHost: strings.Trim(strings.TrimSpace(f.IranHost), "[]"), KharejIP: strings.TrimSpace(f.KharejIP),
		IranTunnel: strings.TrimSpace(f.IranTunnel), KharejTunnel: strings.TrimSpace(f.KharejTunnel),
		IPXProfile: f.IPXProfile, Ports: strings.FieldsFunc(f.Ports, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
		}),
	}
	if backhaulpremium.IsTUN(c.Mode) {
		c.HealthPort = uint16(ctPickPort(map[int]bool{int(c.Port): true}, true))
	}
	service, active, err := installBackhaul(ctx, f.Name, c)
	if err != nil {
		return BackhaulSetup{}, err
	}
	return BackhaulSetup{Service: service, Active: active, Apply: backhaulJoinCommand(backhaulPeerToken(f.Name, c))}, nil
}

func backhaulPeerToken(name string, c config.BackhaulConfig) string {
	c.Role, c.Ports = "client", nil
	body, _ := json.Marshal(backhaulLink{Name: name, Side: c})
	return base64.RawURLEncoding.EncodeToString(body)
}

func backhaulJoinCommand(token string) string {
	return "printf '%s\\n' '" + token + "' | sudo backpack backhaul join"
}

func JoinBackhaul(ctx context.Context, token string) (string, bool, error) {
	var link backhaulLink
	if len(token) > 16<<10 {
		return "", false, errors.New("Backhaul setup token is too long")
	}
	body, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || json.Unmarshal(body, &link) != nil || link.Side.Role != "client" || len(link.Side.Ports) != 0 {
		return "", false, errors.New("invalid Backhaul setup token")
	}
	return installBackhaul(ctx, link.Name, link.Side)
}

func installBackhaul(ctx context.Context, name string, c config.BackhaulConfig) (string, bool, error) {
	if err := core.CheckName(name); err != nil {
		return "", false, err
	}
	if err := backhaulpremium.Validate(c); err != nil {
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
	}{Backhaul: c}, backhaulpremium.Ensure)
}
