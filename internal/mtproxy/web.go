package mtproxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/gorilla/websocket"
)

const webBootstrapType = "application/vnd.telemt.web-recovery+json"

var webHello = []byte{0x10, 0, 0, 0, 0, 0, 0, 1, 1}
var webWelcome = []byte{0x11, 0, 0, 0, 0, 0, 0, 0}

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
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if s.Mode != ModeWeb {
		return 0, errors.New("this setup is not a WEB proxy")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.checkWebListener(ctx, listen); err != nil {
		return 0, fmt.Errorf("through the private WEB forward: %w", err)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", s.WebHost)
	if err != nil {
		return 0, errors.New("the WEB hostname has no usable DNS record")
	}
	expected, _ := netip.ParseAddr(s.WebIP)
	for _, ip := range ips {
		if ip.Unmap() == expected.Unmap() {
			if err := s.checkWebEndpoint(ctx); err != nil {
				return 0, fmt.Errorf("public WEB endpoint: %w", err)
			}
			return time.Since(start), nil
		}
	}
	return 0, errors.New("the WEB hostname does not resolve to the configured public address")
}

func webHTTPClient(dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	return &http.Client{
		Transport: &http.Transport{DialContext: dial},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (s Setup) checkWebListener(ctx context.Context, listen string) error {
	client := webHTTPClient(nil)
	defer client.CloseIdleConnections()
	_, err := s.webBootstrap(ctx, client, "http://"+listen, true)
	return err
}

func (s Setup) webBootstrap(ctx context.Context, client *http.Client, base string, local bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/?bridge="+s.webCapability(), nil)
	if err != nil {
		return "", errors.New("invalid WEB address")
	}
	req.Host = s.WebHost
	req.Header.Set("Accept", webBootstrapType)
	if local {
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
	}
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("could not connect to the WEB listener or verify its TLS certificate")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != webBootstrapType {
		return "", fmt.Errorf("bridge rejected the configured hostname or secret (HTTP %d)", res.StatusCode)
	}
	var bootstrap struct {
		Version int    `json:"v"`
		Token   string `json:"bootstrap"`
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1025))
	if err != nil || len(body) > 1024 || json.Unmarshal(body, &bootstrap) != nil ||
		bootstrap.Version != 1 || !validWebToken(bootstrap.Token) {
		return "", errors.New("invalid WEB bootstrap response")
	}
	return bootstrap.Token, nil
}

func validWebToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(data) == 32
}

func (s Setup) checkWebEndpoint(ctx context.Context) error {
	addr := net.JoinHostPort(s.WebIP, "443")
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	client := webHTTPClient(dial)
	defer client.CloseIdleConnections()
	base := "https://" + s.WebHost
	bootstrap, err := s.webBootstrap(ctx, client, base, false)
	if err != nil {
		return err
	}
	path := base + "/api/v1/session"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(webHello))
	if err != nil {
		return errors.New("invalid WEB session address")
	}
	req.Header.Set("Authorization", "Bearer "+bootstrap)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := client.Do(req)
	if err != nil {
		return errors.New("could not reach the WEB session route")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(len(webWelcome)+1)))
	res.Body.Close()
	token := res.Header.Get("X-Session-Token")
	if res.StatusCode == http.StatusOK && validWebToken(token) {
		defer closeWebSession(ctx, client, path, token)
	}
	if err != nil || res.StatusCode != http.StatusOK || !bytes.Equal(body, webWelcome) || !validWebToken(token) {
		return fmt.Errorf("WEB session authentication failed (HTTP %d)", res.StatusCode)
	}
	if res.Header.Get("X-Carrier-Mode") != "websocket" {
		return errors.New("WEB session did not negotiate the configured WebSocket carrier")
	}

	protocol := "tproxy-v1." + token
	wss := websocket.Dialer{NetDialContext: dial, Subprotocols: []string{protocol}}
	conn, handshake, err := wss.DialContext(ctx, "wss://"+s.WebHost+"/api/v1/ws", nil)
	if err != nil {
		if handshake != nil {
			handshake.Body.Close()
			return fmt.Errorf("the HTTPS frontend did not upgrade the WEB WebSocket (HTTP %d)", handshake.StatusCode)
		}
		return errors.New("could not connect to the WEB WebSocket")
	}
	defer conn.Close()
	if conn.Subprotocol() != protocol {
		return errors.New("the HTTPS frontend dropped the WEB WebSocket subprotocol")
	}
	return nil
}

func closeWebSession(ctx context.Context, client *http.Client, path, token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if res, err := client.Do(req); err == nil {
		res.Body.Close()
	}
}
