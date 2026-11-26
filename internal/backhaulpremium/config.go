package backhaulpremium

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
)

const Prefix = "backhaul-"

type Option struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
}

var options = []Option{
	{"xtcpmux", "XTcpMux", "reverse"},
	{"xwsmux", "XWsMux", "reverse"},
	{"tun-tcp", "TUN / TCP", "reverse"},
	{"tun-ipx", "TUN / IPX", "direct"},
}

func Options() []Option { return append([]Option(nil), options...) }

func Find(mode string) (Option, bool) {
	for _, option := range options {
		if option.Value == mode {
			return option, true
		}
	}
	return Option{}, false
}

var profiles = []string{"tcp", "udp", "icmp", "gre", "ipip", "bip"}

func Profiles() []string { return append([]string(nil), profiles...) }

func IsTUN(mode string) bool { return mode == "tun-tcp" || mode == "tun-ipx" }

var dnsName = regexp.MustCompile("^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$")
var portRule = regexp.MustCompile("^[A-Za-z0-9.:[\\]-]+([=:][A-Za-z0-9.:[\\]-]+)*$")

func ValidEndpointHost(value string) bool {
	host, err := netip.ParseAddr(value)
	if err != nil {
		return dnsName.MatchString(value)
	}
	return !host.IsUnspecified() && !host.IsMulticast() && host.Zone() == ""
}

func Validate(c config.BackhaulConfig) error {
	if _, ok := Find(c.Mode); !ok {
		return errors.New("unknown Backhaul transport")
	}
	if c.Role != "server" && c.Role != "client" {
		return errors.New("invalid Backhaul endpoint role")
	}
	if c.Token == "" || len(c.Token) > 512 || strings.ContainsAny(c.Token, "\r\n\x00") {
		return errors.New("a Backhaul tunnel token is required")
	}
	if !ValidEndpointHost(c.IranHost) {
		return errors.New("enter the address of the Iran endpoint")
	}
	if c.Mode != "tun-ipx" && c.Port == 0 {
		return errors.New("enter a nonzero Backhaul listener port")
	}
	if c.Mode == "tun-ipx" {
		host, _ := netip.ParseAddr(c.IranHost)
		far, err := netip.ParseAddr(c.KharejIP)
		if err != nil || !host.Is4() || !far.Is4() || host == far || !host.IsGlobalUnicast() || !far.IsGlobalUnicast() {
			return errors.New("IPX needs two distinct IPv4 endpoints")
		}
		known := false
		for _, profile := range profiles {
			known = known || c.IPXProfile == profile
		}
		key, err := base64.StdEncoding.DecodeString(c.Token)
		if !known || err != nil || len(key) != 32 {
			return errors.New("select an IPX profile and use a 32-byte base64 key")
		}
	}
	if IsTUN(c.Mode) {
		near, err := netip.ParsePrefix(c.IranTunnel)
		far, farErr := netip.ParsePrefix(c.KharejTunnel)
		if err != nil || farErr != nil || !near.Addr().Is4() || !far.Addr().Is4() ||
			!near.Addr().IsGlobalUnicast() || !far.Addr().IsGlobalUnicast() || near.Addr() == far.Addr() ||
			near.Masked() != far.Masked() || c.HealthPort == 0 {
			return errors.New("enter distinct IPv4/CIDR tunnel addresses in the same subnet and a health port")
		}
	}
	if c.Role == "client" && len(c.Ports) != 0 {
		return errors.New("Backhaul port mappings belong to Iran")
	}
	for _, rule := range c.Ports {
		if len(rule) > 160 || !portRule.MatchString(rule) {
			return fmt.Errorf("invalid Backhaul TCP mapping %q", rule)
		}
	}
	return nil
}

func DeviceName(name string) string {
	return fmt.Sprintf("bph%x", sha256.Sum256([]byte(name)))[:15]
}

// Build renders the native v2.0.3 TOML. iface is resolved on the endpoint
// that will run the core for IPX.
func Build(name string, c config.BackhaulConfig, iface string) ([]byte, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	transport := map[string]any{"type": c.Mode, "heartbeat_interval": 10, "heartbeat_timeout": 25}
	tuning := map[string]any{"auto_tuning": false}
	doc := map[string]any{
		"transport": transport, "tuning": tuning,
		"logging":  map[string]any{"log_level": "info"},
		"security": map[string]any{"token": c.Token},
	}
	server := c.Role == "server"
	if c.Mode != "tun-ipx" {
		transport["nodelay"], transport["keepalive_period"] = true, 40
		port := strconv.Itoa(int(c.Port))
		if server {
			doc["listener"] = map[string]any{"bind_addr": ":" + port}
		} else {
			doc["dialer"] = map[string]any{"remote_addr": net.JoinHostPort(c.IranHost, port),
				"dial_timeout": 10, "retry_interval": 3}
		}
	}
	if IsTUN(c.Mode) {
		transport["type"] = "tun"
		tuning["channel_size"] = 128
		local, remote, mtu := c.IranTunnel, c.KharejTunnel, 1500
		encap := "tcp"
		if !server {
			local, remote = remote, local
		}
		if c.Mode == "tun-ipx" {
			encap, mtu = "ipx", 1320
			tuning["workers"], tuning["batch_size"] = 1, 32
			near, peer := c.IranHost, c.KharejIP
			if !server {
				near, peer = peer, near
			}
			ipx := map[string]any{"mode": c.Role, "profile": c.IPXProfile, "listen_ip": near, "dst_ip": peer}
			if iface != "" {
				ipx["interface"] = iface
			}
			if c.IPXProfile == "icmp" {
				ipx["icmp_type"], ipx["icmp_code"] = 0, 0
			}
			doc["ipx"] = ipx
			doc["security"] = map[string]any{"enable_encryption": true, "algorithm": "aes-256-gcm",
				"psk": c.Token, "kdf_iterations": 100000}
		}
		doc["tun"] = map[string]any{"encapsulation": encap, "name": DeviceName(name), "local_addr": local,
			"remote_addr": remote, "health_port": c.HealthPort, "mtu": mtu}
	} else {
		if !server {
			transport["connection_pool"] = 8
		}
		doc["mux"] = map[string]any{"mux_version": 2, "mux_concurrency": 8, "mux_framesize": 32768,
			"mux_recievebuffer": 4194304, "mux_streambuffer": 2097152}
	}
	if server {
		mappings := map[string]any{"mapping": append([]string{}, c.Ports...)}
		if IsTUN(c.Mode) {
			mappings["forwarder"] = "backhaul"
		}
		doc["ports"] = mappings
	}
	var buf bytes.Buffer
	err := toml.NewEncoder(&buf).Encode(doc)
	return buf.Bytes(), err
}
