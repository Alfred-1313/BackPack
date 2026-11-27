// Package mtproxy manages the optional Fake TLS proxy on a tunnel's exit.
package mtproxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	LegacyVersion = "v2.3.0"
	Version       = "3.5.17"
	ModeNative    = "native"
	ModeWeb       = "web"
)

type Setup struct {
	Port    int    `json:"port"`
	Secret  string `json:"secret"`
	Mode    string `json:"mode,omitempty"`
	Bind    string `json:"bind,omitempty"`
	Sponsor string `json:"sponsor,omitempty"`
	WebHost string `json:"webHost,omitempty"`
	WebIP   string `json:"webIp,omitempty"`
}

func NewSetup(port int, domain string) (Setup, error) {
	setup, err := NewProxy(ModeNative, port, "", domain, "", "")
	if err == nil {
		setup.Mode = ""
	}
	return setup, err
}

func NewProxy(mode string, port int, bind, domain, ip, sponsor string) (Setup, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if !domainName(domain) {
		return Setup{}, errors.New("enter a TLS hostname such as www.example.com")
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return Setup{}, err
	}
	setup := Setup{Port: port, Bind: bind, Mode: mode, Sponsor: strings.ToLower(strings.TrimSpace(sponsor))}
	if mode == ModeWeb {
		setup.Secret = "dd" + hex.EncodeToString(key)
		setup.WebHost, setup.WebIP = domain, strings.TrimSpace(ip)
	} else {
		setup.Secret = "ee" + hex.EncodeToString(key) + hex.EncodeToString([]byte(domain))
	}
	return setup, setup.Validate()
}

func (s Setup) Validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("MTProxy needs a valid destination port")
	}
	ip, err := netip.ParseAddr(s.BindIP())
	if err != nil || ip.Zone() != "" || !(ip.IsLoopback() || ip.IsPrivate()) {
		return errors.New("MTProxy must bind to the exit's loopback or private tunnel IP")
	}
	if s.Mode == ModeWeb {
		public, err := netip.ParseAddr(s.WebIP)
		if !ip.IsLoopback() || err != nil || public.Zone() != "" || !ValidPublicHost(s.WebIP) || !domainName(s.WebHost) {
			return errors.New("WEB requires a loopback exit, a public domain, and its public IP")
		}
		if len(s.Secret) != 34 || !strings.HasPrefix(s.Secret, "dd") || !validHex(s.Secret[2:]) || s.Sponsor != "" {
			return errors.New("invalid WEB proxy settings")
		}
		return nil
	}
	if s.Mode != "" && s.Mode != ModeNative {
		return errors.New("unknown MTProxy mode")
	}
	if s.Sponsor != "" && !ValidTag(s.Sponsor) {
		return errors.New("enter the 32-hex sponsor tag received from @MTProxybot")
	}
	if len(s.Secret) < 36 || len(s.Secret) > 540 || !strings.HasPrefix(s.Secret, "ee") {
		return errors.New("invalid Fake TLS secret")
	}
	if _, err := hex.DecodeString(s.Secret); err != nil || !domainName(s.domain()) {
		return errors.New("invalid Fake TLS secret")
	}
	return nil
}

func validHex(s string) bool { _, err := hex.DecodeString(s); return err == nil }

func ValidTag(s string) bool { return len(s) == 32 && validHex(s) && s != strings.Repeat("0", 32) }

func (s Setup) BindIP() string {
	if s.Bind == "" {
		return "127.0.0.1"
	}
	return s.Bind
}

func (s Setup) Addr() string { return net.JoinHostPort(s.BindIP(), strconv.Itoa(s.Port)) }

func (s Setup) RegistrationSecret() string { return s.Secret[2:34] }

func (s Setup) domain() string {
	host, _ := hex.DecodeString(s.Secret[34:])
	return string(host)
}

var label = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func domainName(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") || net.ParseIP(s) != nil || strings.HasSuffix(s, ".localhost") {
		return false
	}
	for part := range strings.SplitSeq(s, ".") {
		if !label.MatchString(part) {
			return false
		}
	}
	return true
}

func ValidPublicHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsPrivate()
	}
	return domainName(host)
}

func (s Setup) ID() string {
	digest := sha256.Sum256([]byte(s.Secret))
	return hex.EncodeToString(digest[:8])
}

func (s Setup) Link(host string, port int) string {
	if s.Mode == ModeWeb {
		return "tg://webproxy?" + url.Values{"server": {s.WebHost}, "secret": {s.Secret}}.Encode()
	}
	return "https://t.me/proxy?" + url.Values{
		"server": {host}, "port": {strconv.Itoa(port)}, "secret": {s.Secret},
	}.Encode()
}

func (s Setup) Command() string {
	data, _ := json.Marshal(s)
	return fmt.Sprintf("printf '%%s\\n' '%s' | sudo backpack mtproxy join", base64.RawURLEncoding.EncodeToString(data))
}

func (s Setup) Config() string {
	if s.Mode == "" {
		return fmt.Sprintf("bind-to = %q\nsecret = %q\n", s.Addr(), s.Secret)
	}
	return s.telemtConfig()
}

const helperRoot = "/usr/local/lib/backpack-mtproxy"
const webSite = helperRoot + "/web-site"

func (s Setup) telemtConfig() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[general]\nuse_middle_proxy = %t\n", s.Sponsor != "")
	if s.Sponsor != "" {
		fmt.Fprintf(&b, "ad_tag = %q\n", s.Sponsor)
	}
	fmt.Fprintf(&b, "[general.modes]\nclassic = false\nsecure = %t\ntls = %t\n", s.Mode == ModeWeb, s.Mode == ModeNative)
	b.WriteString("[general.links]\nshow = []\n[server.api]\nenabled = false\n")
	fmt.Fprintf(&b, "[[server.listeners]]\nip = %q\nport = %d\n", s.BindIP(), s.Port)
	if s.Mode == ModeWeb {
		fmt.Fprintf(&b, "transport = \"web\"\nweb_trusted_proxy_cidrs = [\"127.0.0.1/32\"]\n"+
			"[web]\nenabled = true\ncarrier = \"websocket\"\n"+
			"[[web.vhosts]]\nhost = %q\npublic_addr = %q\n"+
			"[web.vhosts.decoy]\nmode = \"static_directory\"\ndirectory = %q\nindex = \"index.html\"\n"+
			"[[web.vhosts.profiles]]\nuser = \"backpack\"\nsecret_mode = \"dd\"\n", s.WebHost, net.JoinHostPort(s.WebIP, "443"), webSite)
	} else {
		fmt.Fprintf(&b, "[censorship]\ntls_domain = %q\nmask = true\ntls_emulation = true\n", s.domain())
	}
	fmt.Fprintf(&b, "[access.users]\nbackpack = %q\n", s.RegistrationSecret())
	return b.String()
}

func (s Setup) TLSFrontend(listen string) string {
	return fmt.Sprintf("%s {\n  log {\n    output discard\n  }\n  reverse_proxy %s {\n    header_up X-Forwarded-For {remote_host}\n    flush_interval -1\n  }\n}", s.WebHost, listen)
}
