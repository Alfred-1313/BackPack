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

const Version = "v2.3.0"

type Setup struct {
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

func NewSetup(port int, domain string) (Setup, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if !domainName(domain) {
		return Setup{}, errors.New("enter a TLS hostname such as www.example.com")
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return Setup{}, err
	}
	setup := Setup{Port: port, Secret: "ee" + hex.EncodeToString(key) + hex.EncodeToString([]byte(domain))}
	return setup, setup.Validate()
}

func (s Setup) Validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("MTProxy needs a valid destination port")
	}
	if len(s.Secret) < 36 || len(s.Secret) > 540 || !strings.HasPrefix(s.Secret, "ee") {
		return errors.New("invalid Fake TLS secret")
	}
	if _, err := hex.DecodeString(s.Secret); err != nil || !domainName(s.domain()) {
		return errors.New("invalid Fake TLS secret")
	}
	return nil
}

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
	return "https://t.me/proxy?" + url.Values{
		"server": {host}, "port": {strconv.Itoa(port)}, "secret": {s.Secret},
	}.Encode()
}

func (s Setup) Command() string {
	data, _ := json.Marshal(s)
	return fmt.Sprintf("printf '%%s\\n' '%s' | sudo backpack mtproxy join", base64.RawURLEncoding.EncodeToString(data))
}

func (s Setup) Config() string {
	return fmt.Sprintf("bind-to = %q\nsecret = %q\n", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Port)), s.Secret)
}
