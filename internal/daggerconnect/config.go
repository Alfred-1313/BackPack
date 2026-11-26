package daggerconnect

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/backpack/backpack/config"
)

const CoreVersion = "v4.3.1"

type Transport struct {
	Value     string `json:"value"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
}

func Options() []Transport {
	return []Transport{
		{"http", "http", "reverse"},
		{"https", "https", "reverse"},
		{"xhttp", "xhttp", "reverse"},
		{"quantum", "quantum", "reverse"},
		{"quantum+", "quantum+", "reverse"},
		{"nova", "nova", "reverse"},
		{"dc6", "dc6", "reverse"},
		{"tun", "tun", "direct"},
	}
}

var domainPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func checkIP(input string, version int, field string) error {
	ip, err := netip.ParseAddr(input)
	if err != nil || ip.Is4() != (version == 4) || ip.Is4In6() || ip.Zone() != "" ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		input == "255.255.255.255" {
		return fmt.Errorf("enter a unicast IPv%d address for %s", version, field)
	}
	return nil
}

type portMap struct {
	Type   string `json:"type"`
	Bind   string `json:"bind"`
	Target string `json:"target"`
}

func parsePort(v string) (string, bool) {
	port, err := strconv.ParseUint(v, 10, 16)
	return strconv.FormatUint(port, 10), err == nil && port > 0
}

func parseMaps(raw, bindIP string) ([]portMap, error) {
	maps := []portMap{}
	seen := make(map[string]bool)
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' }) {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		kind, mapping, ok := strings.Cut(field, ":")
		if !ok {
			kind, mapping = "tcp", field
		}
		if kind != "tcp" && kind != "udp" {
			return nil, fmt.Errorf("forwarded ports use tcp: or udp:")
		}
		bind, target, mapped := strings.Cut(mapping, "=")
		if !mapped {
			target = bind
		}
		bind, bindOK := parsePort(bind)
		target, targetOK := parsePort(target)
		if !bindOK || !targetOK {
			return nil, fmt.Errorf("forwarded ports must be between 1 and 65535")
		}
		key := kind + ":" + bind
		if seen[key] {
			return nil, fmt.Errorf("forwarded port %s is listed twice", key)
		}
		seen[key] = true
		maps = append(maps, portMap{kind, net.JoinHostPort(bindIP, bind), "127.0.0.1:" + target})
	}
	return maps, nil
}

// Build emits the launcher configuration using the schema of the official installer.
func Build(name string, c config.DaggerConfig) ([]byte, error) {
	if c.Role != "server" && c.Role != "client" {
		return nil, fmt.Errorf("the DaggerConnect role must be server or client")
	}
	known := false
	for _, option := range Options() {
		if option.Value == c.Flavor {
			known = true
			break
		}
	}
	if !known && c.Flavor != "quantum-gaming" {
		return nil, fmt.Errorf("unknown DaggerConnect transport")
	}
	if c.Port == 0 {
		return nil, fmt.Errorf("the DaggerConnect port must be between 1 and 65535")
	}
	if c.License == "" || len(c.License) > 4096 || strings.ContainsAny(c.License, "\x00\r\n") {
		return nil, fmt.Errorf("enter the DaggerConnect license PSK")
	}
	if err := checkIP(c.IranIP, 4, "Iran"); err != nil {
		return nil, err
	}
	port := strconv.Itoa(int(c.Port))
	server := c.Role == "server"
	tr := c.Flavor
	if tr == "quantum-gaming" {
		tr = "quantum"
	}
	listen := net.JoinHostPort("0.0.0.0", port)
	peerAddr := net.JoinHostPort(c.IranIP, port)

	profile := fmt.Sprintf("bp-%x", sha256.Sum256([]byte(name)))[:19]
	entry := map[string]any{"transport": tr}
	doc := map[string]any{"mode": c.Role, "transport": tr, "psk": c.License, "log_level": "info", "profile_id": profile}
	switch tr {
	case "quantum":
		tuning := map[string]any{"mtu": 1350, "block": "aes"}
		if c.Flavor == "quantum-gaming" {
			tuning["profile"] = "gaming"
		}
		doc["quantum"] = tuning
	case "dc6":
		if err := checkIP(c.IranIPv6, 6, "Iran's DC6 endpoint"); err != nil {
			return nil, err
		}
		listen = net.JoinHostPort(c.IranIPv6, port)
		if !server {
			entry["ipv6_addr"] = listen
		}
	case "xhttp":
		if !validHTTPPath(c.HTTPPath) {
			return nil, fmt.Errorf("enter an HTTP path starting with /")
		}
		delete(entry, "transport")
		doc["xhttp"] = map[string]any{"path": c.HTTPPath, "mode": "auto",
			"up_max_bytes": 65536, "up_concurrency": 2, "buffer_bytes": 32768,
			"separate_conns": true, "probe_ms": 8000, "socket_buf_bytes": 131072}
	case "http", "https":
		if !domainPattern.MatchString(c.HTTPHost) {
			return nil, fmt.Errorf("enter the HTTP host name")
		}
		if !validHTTPPath(c.HTTPPath) {
			return nil, fmt.Errorf("enter an HTTP path starting with /")
		}
		doc["http_settings"] = map[string]any{"fake_domain": c.HTTPHost, "path": c.HTTPPath}
		if tr == "https" && server {
			if !path.IsAbs(c.CertFile) || !path.IsAbs(c.KeyFile) {
				return nil, fmt.Errorf("enter absolute paths to the HTTPS certificate and private key")
			}
			entry["cert_file"], entry["key_file"] = c.CertFile, c.KeyFile
		}
	case "tun":
		if err := setTUN(doc, profile, c, server); err != nil {
			return nil, err
		}
	}

	if server {
		bindIP := "0.0.0.0"
		if c.LoopbackOnly {
			bindIP = "127.0.0.1"
		}
		maps, err := parseMaps(c.Ports, bindIP)
		if err != nil {
			return nil, err
		}
		entry["addr"], entry["maps"] = listen, maps
		doc["listeners"] = []map[string]any{entry}
	} else {
		entry["addr"], entry["retry_interval"], entry["dial_timeout"] = peerAddr, 3, 10
		if tr == "tun" {
			entry["dial_timeout"] = 30
		} else {
			entry["connection_pool"] = 6
		}
		doc["paths"] = []map[string]any{entry}
	}
	return json.Marshal(doc)
}

func validHTTPPath(path string) bool {
	return strings.HasPrefix(path, "/") && len(path) <= 512 && !strings.ContainsAny(path, " \t\r\n\x00")
}

func setTUN(doc map[string]any, name string, c config.DaggerConfig, server bool) error {
	if err := checkIP(c.KharejIP, 4, "Kharej"); err != nil {
		return err
	}
	for _, field := range []struct{ address, label string }{
		{c.IranTunnelIP, "Iran inside the tunnel"},
		{c.KharejTunnelIP, "Kharej inside the tunnel"},
	} {
		if err := checkIP(field.address, 4, field.label); err != nil {
			return err
		}
	}
	if c.IranIP == c.KharejIP || c.IranTunnelIP == c.KharejTunnelIP {
		return fmt.Errorf("the two TUN endpoints need different addresses")
	}
	local, remote := c.IranIP, c.KharejIP
	inner, other := c.IranTunnelIP, c.KharejTunnelIP
	if !server {
		local, remote, inner, other = remote, local, other, inner
	}
	ipx := map[string]any{"mode": c.Role, "profile": c.TUNProfile, "listen_ip": local, "dst_ip": remote, "sock_buf": 0}
	switch c.TUNProfile {
	case "tcp", "udp":
		if c.TUNPort == 0 {
			return fmt.Errorf("enter the TUN service port")
		}
		ipx["l4_port"] = c.TUNPort
	case "icmp", "gre", "ipip", "bip":
	default:
		return fmt.Errorf("choose a TUN encapsulation")
	}
	doc["ipx"] = ipx
	doc["tun"] = map[string]any{"encapsulation": "ipx", "name": "d" + name[3:15],
		"local_addr": inner, "remote_addr": other, "encrypt": true, "heartbeat_sec": 10, "idle_timeout_sec": 90}
	return nil
}
