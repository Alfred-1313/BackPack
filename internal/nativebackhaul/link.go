package nativebackhaul

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/tunnel/portmap"
	"github.com/gorilla/websocket"
)

const linkTimeout = 8 * time.Second

// TLS authenticates both endpoints against a tunnel-specific identity.
func linkTLS(c config.BackhaulConfig) (*tls.Config, error) {
	raw, err := base64.StdEncoding.DecodeString(c.Token)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("native tunnel token must encode 32 random bytes")
	}
	seed := sha256.Sum256(append([]byte("BackPack native tunnel TLS "+c.Mode+"\x00"), raw...))
	key := ed25519.NewKeyFromSeed(seed[:])
	serial := sha256.Sum256(key.Public().(ed25519.PublicKey))
	tmpl := x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(append([]byte{1}, serial[:15]...)),
		NotBefore:    time.Unix(1577836800, 0), NotAfter: time.Unix(4102444800, 0),
		DNSNames:    []string{"tunnel.backpack.invalid"},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	cert, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert}, PrivateKey: key, Leaf: leaf}},
		RootCAs:      roots, ServerName: tmpl.DNSNames[0],
		ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert,
		SessionTicketsDisabled: true,
	}, nil
}

func Options() []backhaulpremium.Option {
	return []backhaulpremium.Option{
		{Value: "xtcpmux", Label: "XTcpMux", Direction: "reverse"},
		{Value: "xwsmux", Label: "XWsMux", Direction: "reverse"},
		{Value: "tun-tcp", Label: "TUN / TCP", Direction: "reverse"},
		{Value: "dagger-http", Label: "HTTP Upgrade", Direction: "reverse"},
		{Value: "dagger-https", Label: "HTTPS Upgrade", Direction: "reverse"},
		{Value: "dagger-xhttp", Label: "Split HTTP", Direction: "reverse"},
	}
}

func Find(mode string) (backhaulpremium.Option, bool) {
	for _, option := range Options() {
		if mode == option.Value {
			return option, true
		}
	}
	return backhaulpremium.Option{}, false
}

func Validate(c config.BackhaulConfig) error {
	if _, ok := Find(c.Mode); !ok {
		return errors.New("unknown native BackPack tunnel mode")
	}
	if c.Lanes < 0 || c.Lanes > 16 || c.PacketQueue < 0 || c.PacketQueue > 1024 ||
		c.MTU != 0 && (c.MTU < 576 || c.MTU > tunMTU) {
		return errors.New("native tunnel tuning is outside its supported range")
	}
	if c.Mode == "tun-tcp" && c.Lanes != 0 || c.Mode != "tun-tcp" && (c.PacketQueue != 0 || c.MTU != 0) {
		return errors.New("native tunnel tuning does not match this transport")
	}
	if err := validateMuxOptions(c); err != nil {
		return err
	}
	if err := validateHTTPOptions(c); err != nil {
		return err
	}
	// Validate the shared reverse-tunnel settings in the same way for every
	// stream transport. The TUN-specific fields are checked only for TUN.
	common := c
	if c.Mode != "tun-tcp" {
		common.Mode = "xtcpmux"
	}
	if c.HealthPort == 0 {
		common.HealthPort = 1
	} // native TUN has no separate health listener
	if err := backhaulpremium.Validate(common); err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(c.Token)
	if err != nil || len(raw) != 32 {
		return errors.New("native tunnel token must encode 32 random bytes")
	}
	defaultHost := "127.0.0.1"
	if c.Mode == "tun-tcp" {
		defaultHost, _, _ = strings.Cut(c.KharejTunnel, "/")
	}
	_, err = portmap.Expand(c.Ports, defaultHost)
	return err
}

func listenLanes(ctx context.Context, c config.BackhaulConfig, addr string, t *tls.Config, handle func(net.Conn)) error {
	network := "tcp"
	if c.Mux != nil && c.Mux.IPv6 != nil && !*c.Mux.IPv6 {
		network = "tcp4"
	}
	listener, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	defer listener.Close()
	if c.Mode == "dagger-xhttp" {
		return serveSplitHTTP(ctx, listener, c, t, handle)
	}
	if c.Mode == "xwsmux" {
		upgrader := websocket.Upgrader{HandshakeTimeout: linkTimeout}
		server := http.Server{TLSConfig: t, ReadHeaderTimeout: linkTimeout, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/tunnel" {
				http.NotFound(w, r)
				return
			}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err == nil {
				ws.SetReadLimit(64 << 10)
				handle(&wsConn{Conn: ws})
			}
		})}
		stopHTTP := context.AfterFunc(ctx, func() { server.Close() })
		defer stopHTTP()
		err = server.ServeTLS(listener, "", "")
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	slots := make(chan struct{}, 32)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			go func() {
				hctx, cancel := context.WithTimeout(ctx, linkTimeout)
				lane, err := admitLane(hctx, c, conn, t)
				cancel()
				<-slots
				if err != nil {
					conn.Close()
					return
				}
				handle(lane)
			}()
		default:
			conn.Close()
		}
	}
}

func dialLane(ctx context.Context, c config.BackhaulConfig, addr string, t *tls.Config) (net.Conn, error) {
	dial := &net.Dialer{Timeout: linkTimeout, KeepAlive: 15 * time.Second}
	switch c.Mode {
	case "dagger-xhttp":
		return dialSplitHTTP(ctx, dial, addr, c, t)
	case "dagger-http", "dagger-https":
		return dialUpgrade(ctx, dial, c, addr, t)
	}
	if c.Mode != "xwsmux" {
		td := tls.Dialer{NetDialer: dial, Config: t}
		return td.DialContext(ctx, "tcp", addr)
	}
	wd := websocket.Dialer{TLSClientConfig: t, NetDialContext: dial.DialContext, HandshakeTimeout: linkTimeout}
	u := url.URL{Scheme: "wss", Host: addr, Path: "/tunnel"}
	ws, response, err := wd.DialContext(ctx, u.String(), nil)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		return nil, err
	}
	ws.SetReadLimit(64 << 10)
	return &wsConn{Conn: ws}, nil
}

// Each lane exposes a single reader and writer to smux. Gorilla handles
// control frames and fragmentation on that reader.
type wsConn struct {
	*websocket.Conn
	reader io.Reader
}

func (w *wsConn) Read(b []byte) (int, error) {
	for {
		if w.reader == nil {
			kind, reader, err := w.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, fmt.Errorf("unexpected WebSocket frame type %d", kind)
			}
			w.reader = reader
		}
		n, err := w.reader.Read(b)
		if err == io.EOF {
			w.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
func (w *wsConn) Write(b []byte) (int, error) {
	if err := w.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}
func (w *wsConn) SetDeadline(deadline time.Time) error {
	return errors.Join(w.SetReadDeadline(deadline), w.SetWriteDeadline(deadline))
}
