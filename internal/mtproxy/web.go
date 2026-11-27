package mtproxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// webCapability implements the root WEB vhost credential format used by
// Telegram Desktop and Telemt. The key includes the dd prefix.
func (s Setup) webCapability() string {
	key, _ := hex.DecodeString(s.Secret)
	mac := hmac.New(sha256.New, key)
	io.WriteString(mac, "tdesktop-web-proxy-bridge-v1\n")
	io.WriteString(mac, s.WebHost)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s Setup) CheckWeb(ctx context.Context, listen string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.checkBridge(ctx, "http://"+listen); err != nil {
		return 0, fmt.Errorf("private WEB listener: %w", err)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", s.WebHost)
	if err != nil {
		return 0, errors.New("the WEB hostname has no usable DNS record")
	}
	expected, _ := netip.ParseAddr(s.WebIP)
	for _, ip := range ips {
		if ip.Unmap() == expected.Unmap() {
			if err := s.checkBridge(ctx, "https://"+s.WebHost); err != nil {
				return 0, fmt.Errorf("public TLS endpoint: %w", err)
			}
			return time.Since(start), nil
		}
	}
	return 0, errors.New("the WEB hostname does not resolve to the configured public address")
}

func (s Setup) checkBridge(ctx context.Context, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/?bridge="+s.webCapability(), nil)
	if err != nil {
		return errors.New("invalid WEB address")
	}
	req.Host = s.WebHost
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	if req.URL.Scheme == "https" {
		addr := net.JoinHostPort(s.WebIP, "443")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("could not connect or validate TLS")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 512<<10))
	if err != nil || res.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("/api/v1/session")) {
		return fmt.Errorf("bridge authentication did not return the Telegram WEB page (HTTP %d)", res.StatusCode)
	}
	return nil
}
