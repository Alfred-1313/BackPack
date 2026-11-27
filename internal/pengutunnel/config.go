package pengutunnel

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
)

const Prefix = "pengu-"
const Label = "PenguTunnel"

type Mode struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Carrier string `json:"-"`
	IPX     bool   `json:"ipx"`
	TUN     bool   `json:"tun"`
}

var modes = []Mode{
	{ID: "tcpfwd", Label: "tcpfwd", Carrier: "tcpfwd"},
	{ID: "tls", Label: "TLS", Carrier: "tls"},
	{ID: "num1", Label: "PGprotocol-NUM1", Carrier: "PGprotocol-NUM1", TUN: true},
	{ID: "num2", Label: "PGprotocol-NUM2", Carrier: "PGprotocol-NUM2", TUN: true},
	{ID: "num3", Label: "PGprotocol-NUM3", Carrier: "PGprotocol-NUM3", TUN: true},
	{ID: "icmp", Label: "TUN / IPX ICMP", Carrier: "ipx", IPX: true, TUN: true},
	{ID: "ipip", Label: "TUN / IPX IPIP", Carrier: "ipx", IPX: true, TUN: true},
	{ID: "ospf", Label: "TUN / IPX OSPF", Carrier: "ipx", IPX: true, TUN: true},
	{ID: "gre", Label: "TUN / IPX GRE", Carrier: "ipx", IPX: true, TUN: true},
	{ID: "ah", Label: "TUN / IPX AH", Carrier: "ipx", IPX: true, TUN: true},
	{ID: "esp", Label: "TUN / IPX ESP", Carrier: "ipx", IPX: true, TUN: true},
}

func Options() []Mode { return slices.Clone(modes) }

func Find(id string) (Mode, bool) {
	for _, mode := range modes {
		if mode.ID == id {
			return mode, true
		}
	}
	return Mode{}, false
}

// Listens determines the listener for transports with a stream port.
func Listens(c config.PenguConfig) bool {
	return (c.Direction == "reverse") == (c.Side == "iran")
}

func DeviceName(name string) string {
	return fmt.Sprintf("bpg%x", sha256.Sum256([]byte(name)))[:15]
}

var hostname = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
var forward = regexp.MustCompile(`^(udp/)?(?:(127\.0\.0\.1):)?([0-9]{1,5}(?:-[0-9]{1,5})?)(?:=(?:(127\.0\.0\.1):)?([0-9]{1,5}(?:-[0-9]{1,5})?))?$`)

func validHost(host, family string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsGlobalUnicast() && ip.Zone() == "" && ip.Unmap().Is4() == (family == "ipv4")
	}
	return hostname.MatchString(host)
}

func portRange(value string) (int, bool) {
	first, last, ok := strings.Cut(value, "-")
	a, err := strconv.Atoi(first)
	if err != nil || a < 1 || a > 65535 {
		return 0, false
	}
	if !ok {
		return 1, true
	}
	b, err := strconv.Atoi(last)
	return b - a + 1, err == nil && b >= a && b <= 65535
}

func validatePorts(c config.PenguConfig, tun bool) error {
	if c.Side == "kharej" && len(c.Ports) != 0 || c.Side == "iran" && !tun && len(c.Ports) == 0 {
		return errors.New("a No-TUN edge needs at least one forward, and forwards belong to Iran")
	}
	total := 0
	for _, rule := range c.Ports {
		m := forward.FindStringSubmatch(rule)
		if m == nil || tun && (m[2] != "" || m[4] != "") {
			return fmt.Errorf("invalid PenguTunnel forward %q", rule)
		}
		count, ok := portRange(m[3])
		if !ok {
			return fmt.Errorf("invalid port range %q", m[3])
		}
		if m[5] != "" {
			target, ok := portRange(m[5])
			if !ok || target != count {
				return fmt.Errorf("forward %q needs equally sized port ranges", rule)
			}
		}
		total += count
		if total > 512 {
			return errors.New("PenguTunnel supports at most 512 configured forwards per tunnel")
		}
	}
	return nil
}

func Validate(c config.PenguConfig) error {
	mode, ok := Find(c.Mode)
	if !ok || !slices.Contains([]string{"iran", "kharej"}, c.Side) ||
		!slices.Contains([]string{"reverse", "direct"}, c.Direction) ||
		!slices.Contains([]string{"ipv4", "ipv6"}, c.Family) {
		return errors.New("invalid PenguTunnel transport, direction, or address family")
	}
	key, err := base64.StdEncoding.DecodeString(c.Key)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != c.Key {
		return errors.New("PenguTunnel needs a 32-byte base64 security token")
	}
	if !validHost(c.IranHost, c.Family) || (c.Direction == "direct" || mode.IPX) && !validHost(c.KharejHost, c.Family) {
		return errors.New("enter valid PenguTunnel endpoint addresses for the chosen address family")
	}
	if mode.IPX {
		iran, _ := netip.ParseAddr(c.IranHost)
		kharej, _ := netip.ParseAddr(c.KharejHost)
		if c.Direction != "direct" || !iran.Is4() || !kharej.Is4() || iran == kharej || c.Port != 0 {
			return errors.New("PenguTunnel IPX needs distinct IPv4 endpoints")
		}
	} else if c.Port == 0 {
		return errors.New("enter a PenguTunnel listener port")
	}
	if c.HealthPort == 0 || !mode.IPX && c.HealthPort == c.Port ||
		c.Pool < 1 || c.Pool > 32 || c.Workers < 0 || c.Workers > 64 || c.Window < 0 || c.Window > 256 {
		return errors.New("invalid PenguTunnel health port or performance settings")
	}
	if mode.TUN {
		near, nearErr := netip.ParsePrefix(c.IranTunnel)
		far, farErr := netip.ParsePrefix(c.KharejTunnel)
		if nearErr != nil || farErr != nil || !near.Addr().Is4() || !far.Addr().Is4() ||
			!near.Addr().IsGlobalUnicast() || !far.Addr().IsGlobalUnicast() ||
			near.Masked() != far.Masked() || near.Addr() == far.Addr() || c.MTU < 576 || c.MTU > 1500 {
			return errors.New("enter distinct TUN IPv4/CIDR addresses in one subnet and an MTU from 576 to 1500")
		}
	}
	if !slices.Contains([]string{"balance", "turbo", "aggressive"}, c.Preset) ||
		!slices.Contains([]string{"aes-256-gcm", "chacha20-poly1305"}, c.Cipher) {
		return errors.New("invalid PenguTunnel tuning profile or cipher")
	}
	if c.Header != "" && (c.Mode != "tcpfwd" ||
		!slices.Contains([]string{"PGprotocol-NUM1", "PGprotocol-NUM2", "PGprotocol-NUM3"}, c.Header)) {
		return errors.New("invalid tcpfwd carrier header")
	}
	if c.TLSName != "" && (c.Mode != "tls" || !hostname.MatchString(c.TLSName)) {
		return errors.New("enter a hostname for the PenguTunnel TLS server name")
	}
	if c.ICMPType != nil && (c.Mode != "icmp" || *c.ICMPType < 0 || *c.ICMPType > 255) ||
		c.ICMPCode < 0 || c.ICMPCode > 255 || c.Mode != "icmp" && c.ICMPCode != 0 {
		return errors.New("invalid PenguTunnel ICMP type or code")
	}
	return validatePorts(c, mode.TUN)
}

// Build renders the core's native TOML for one end of a tunnel.
func Build(name string, c config.PenguConfig, iface, license string) ([]byte, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	mode, _ := Find(c.Mode)
	role, local, peer, addr := "server", c.IranHost, c.KharejHost, c.IranTunnel
	if c.Side == "kharej" {
		role, local, peer, addr = "client", c.KharejHost, c.IranHost, c.KharejTunnel
	}
	if mode.TUN && !mode.IPX && c.Direction == "direct" {
		if c.Side == "iran" {
			role = "client"
		} else {
			role = "server"
		}
	}
	transport := map[string]any{"type": mode.Carrier, "mode": role}
	tun := map[string]any{"health_port": c.HealthPort}
	tuning := map[string]any{"auto_tuning": true, "workers": c.Workers,
		"profile": map[string]string{"balance": "resource", "turbo": "balance", "aggressive": "fast"}[c.Preset]}
	doc := map[string]any{
		"transport": transport, "tun": tun, "tuning": tuning,
		"health":   map[string]any{"heartbeat_interval": 10, "heartbeat_timeout": 25},
		"logging":  map[string]any{"level": "info"},
		"security": map[string]any{"enable_encryption": true, "algorithm": c.Cipher, "psk": c.Key},
	}
	if mode.IPX {
		transport["rekey_interval"] = 200
		ipx := map[string]any{"mode": role, "profile": mode.ID, "listen_ip": local, "dst_ip": peer}
		if iface != "" {
			ipx["interface"] = iface
		}
		if mode.ID == "icmp" {
			if c.ICMPType != nil {
				ipx["icmp_type"] = *c.ICMPType
			}
			ipx["icmp_code"] = c.ICMPCode
		}
		doc["ipx"] = ipx
	} else {
		port := strconv.Itoa(int(c.Port))
		transport["pool_connection"] = c.Pool
		if Listens(c) {
			host := "0.0.0.0"
			if c.Family == "ipv6" {
				host = "::"
			}
			transport["listen"] = net.JoinHostPort(host, port)
		} else {
			transport["remote"] = net.JoinHostPort(peer, port)
			if c.TLSName != "" {
				transport["sni"] = c.TLSName
			}
		}
	}
	if mode.TUN {
		tun["name"], tun["address"], tun["mtu"] = DeviceName(name), addr, c.MTU
	} else {
		tuning["stream_window"] = c.Window
		if c.Direction == "direct" {
			transport["l4_direction"] = "direct"
		}
		if c.Header != "" {
			transport["l4_header"] = c.Header
		}
	}
	if len(c.Ports) != 0 {
		doc["forward"] = map[string]any{"proxy_protocol": false, "ports": c.Ports}
	}
	if c.Side == "kharej" && license != "" {
		doc["license"] = map[string]any{"key": license}
	}
	var out bytes.Buffer
	err := toml.NewEncoder(&out).Encode(doc)
	return out.Bytes(), err
}
