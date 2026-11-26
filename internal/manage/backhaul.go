package manage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/nativebackhaul"
)

type BackhaulForm struct {
	Name         string      `json:"name"`
	Mode         string      `json:"transport"`
	IranHost     string      `json:"iranHost"`
	KharejIP     string      `json:"kharejIP"`
	Port         uint16      `json:"port"`
	Secret       string      `json:"secret"`
	Ports        string      `json:"ports"`
	IranTunnel   string      `json:"iranTunnel"`
	KharejTunnel string      `json:"kharejTunnel"`
	IPXProfile   string      `json:"ipxProfile"`
	Tuning       SetupTuning `json:"tuning"`
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

func CreateBackhaul(ctx context.Context, f BackhaulForm) (BackhaulSetup, error) {
	value, err := setupTuningValue("native-"+f.Mode, f.Tuning)
	if err != nil {
		return BackhaulSetup{}, err
	}
	host := strings.Trim(strings.TrimSpace(f.IranHost), "[]")
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
		IPXProfile: f.IPXProfile, Ports: strings.FieldsFunc(f.Ports, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
		}),
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
	if c.Mode == "tun-tcp" && c.IranTunnel == "" && c.KharejTunnel == "" {
		for i := range 16 {
			// Draw a fresh /30 for each attempt. Active interfaces are also
			// checked on the peer before applying its setup command.
			c.IranTunnel, c.KharejTunnel = ctBackhaulPair(f.Name + token + fmt.Sprint(i))
			if backhaulTUNAvailable(c.IranTunnel) == nil {
				break
			}
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
