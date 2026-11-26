package nativebackhaul

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
)

const (
	httpPath        = "/tunnel"
	upgradeProtocol = "backpack"
	httpHeaderLimit = 8 << 10
)

var httpRoute = regexp.MustCompile(`^/[a-zA-Z0-9._~/-]{0,255}$`)

func validateHTTPOptions(c config.BackhaulConfig) error {
	upgrade := c.Mode == "dagger-http" || c.Mode == "dagger-https"
	if c.HTTPPath != "" {
		if !upgrade && c.Mode != "dagger-xhttp" {
			return fmt.Errorf("HTTP path is only available on the native Dagger HTTP transports")
		}
		if !httpRoute.MatchString(c.HTTPPath) || path.Clean(c.HTTPPath) != c.HTTPPath {
			return fmt.Errorf("enter an absolute HTTP path such as /api/v2, without a query or trailing slash")
		}
	}
	if c.HTTPHost != "" {
		if !upgrade {
			return fmt.Errorf("an HTTP Host override is available on Dagger http and https")
		}
		if strings.Contains(c.HTTPHost, ":") || !backhaulpremium.ValidEndpointHost(c.HTTPHost) {
			return fmt.Errorf("enter an HTTP Host name or IPv4 address without a scheme or port")
		}
	}
	return nil
}

func requestPath(c config.BackhaulConfig) string {
	if c.HTTPPath != "" {
		return c.HTTPPath
	}
	return httpPath
}

type bufferedLane struct {
	net.Conn
	reader *bufio.Reader
}

func (l *bufferedLane) Read(b []byte) (int, error) { return l.reader.Read(b) }

func admitLane(ctx context.Context, c config.BackhaulConfig, conn net.Conn, t *tls.Config) (net.Conn, error) {
	if c.Mode == "dagger-http" {
		lane, err := exchangeUpgrade(ctx, conn, c, "")
		if err != nil {
			return nil, err
		}
		conn = lane
	}
	lane := tls.Server(conn, t)
	if err := lane.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	if c.Mode == "dagger-https" {
		return exchangeUpgrade(ctx, lane, c, "")
	}
	return lane, nil
}

func dialUpgrade(ctx context.Context, dial *net.Dialer, c config.BackhaulConfig, addr string, t *tls.Config) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, linkTimeout)
	defer cancel()
	conn, err := dial.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if c.Mode == "dagger-https" {
		conn = tls.Client(conn, t)
	}
	lane, err := exchangeUpgrade(ctx, conn, c, addr)
	if err == nil && c.Mode == "dagger-http" {
		secure := tls.Client(lane, t)
		err = secure.HandshakeContext(ctx)
		lane = secure
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return lane, nil
}

func exchangeUpgrade(ctx context.Context, conn net.Conn, c config.BackhaulConfig, addr string) (net.Conn, error) {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	limit := &io.LimitedReader{R: conn, N: httpHeaderLimit}
	reader := bufio.NewReader(limit)
	if addr == "" {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return nil, err
		}
		if req.Method != http.MethodGet || req.URL.RequestURI() != requestPath(c) || req.ContentLength != 0 ||
			(c.HTTPHost != "" && !strings.EqualFold(req.Host, c.HTTPHost)) ||
			len(req.TransferEncoding) != 0 || !isUpgrade(req.Header) {
			return nil, errors.New("unexpected tunnel HTTP request")
		}
		if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+upgradeProtocol+"\r\n\r\n"); err != nil {
			return nil, err
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+requestPath(c), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", upgradeProtocol)
		if c.HTTPHost != "" {
			req.Host = c.HTTPHost
		}
		if err := req.Write(conn); err != nil {
			return nil, err
		}
		res, err := http.ReadResponse(reader, req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusSwitchingProtocols || !isUpgrade(res.Header) {
			return nil, errors.New("peer rejected the tunnel HTTP upgrade")
		}
	}
	limit.N = math.MaxInt64
	return &bufferedLane{Conn: conn, reader: reader}, nil
}

func isUpgrade(h http.Header) bool {
	return strings.EqualFold(h.Get("Connection"), "Upgrade") && strings.EqualFold(h.Get("Upgrade"), upgradeProtocol)
}

type httpPeer string

func (a httpPeer) Network() string { return "tcp" }
func (a httpPeer) String() string  { return string(a) }

type splitLane struct {
	net.Conn
	local, remote net.Addr
}

func (l *splitLane) LocalAddr() net.Addr  { return l.local }
func (l *splitLane) RemoteAddr() net.Addr { return l.remote }

type splitSession struct {
	ctx    context.Context
	wire   net.Conn
	upload bool
}

// A single lane has two HTTP bodies. The TLS records read and written over
// them authenticate both peers and keep forwarded data encrypted.
func serveSplitHTTP(ctx context.Context, listener net.Listener, c config.BackhaulConfig, t *tls.Config, handle func(net.Conn)) error {
	var mu sync.Mutex
	sessions := make(map[string]*splitSession)
	prefix := strings.TrimSuffix(requestPath(c), "/") + "/"
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: linkTimeout,
		IdleTimeout:       linkTimeout,
		MaxHeaderBytes:    httpHeaderLimit,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok || len(id) != 32 || !hmac.Equal([]byte(r.Header.Get("X-Tunnel-Session")), []byte(splitProof(c.Token, id, r.Method))) {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case http.MethodGet:
				mu.Lock()
				if sessions[id] != nil || len(sessions) >= 32 {
					mu.Unlock()
					w.WriteHeader(http.StatusConflict)
					return
				}
				end, wire := net.Pipe()
				sessionCtx, endSession := context.WithCancel(r.Context())
				sessions[id] = &splitSession{ctx: sessionCtx, wire: wire}
				mu.Unlock()
				defer func() {
					endSession()
					mu.Lock()
					delete(sessions, id)
					mu.Unlock()
					end.Close()
					wire.Close()
				}()
				stop := context.AfterFunc(r.Context(), func() { wire.Close() })
				defer stop()
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Cache-Control", "no-store")
				controller := http.NewResponseController(w)
				if err := controller.Flush(); err != nil {
					return
				}
				lane := tls.Server(&splitLane{Conn: end, local: listener.Addr(), remote: httpPeer(r.RemoteAddr)}, t)
				go func() {
					defer lane.Close()
					hctx, cancel := context.WithTimeout(r.Context(), linkTimeout)
					err := lane.HandshakeContext(hctx)
					cancel()
					if err == nil {
						handle(lane)
					}
				}()
				_, _ = io.Copy(flushingBody{controller, w}, wire)
			case http.MethodPost:
				mu.Lock()
				s := sessions[id]
				if s == nil || s.upload {
					mu.Unlock()
					w.WriteHeader(http.StatusConflict)
					return
				}
				s.upload = true
				mu.Unlock()
				controller := http.NewResponseController(w)
				stop := context.AfterFunc(s.ctx, func() { _ = controller.SetReadDeadline(time.Now()) })
				defer stop()
				defer s.wire.Close()
				_, _ = io.Copy(s.wire, r.Body)
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		}),
	}
	server.SetKeepAlivesEnabled(false)
	stop := context.AfterFunc(ctx, func() { server.Close() })
	defer stop()
	err := server.Serve(listener)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

type flushingBody struct {
	controller *http.ResponseController
	writer     http.ResponseWriter
}

func (w flushingBody) Write(b []byte) (int, error) {
	_ = w.controller.SetWriteDeadline(time.Now().Add(linkTimeout))
	n, err := w.writer.Write(b)
	if err == nil {
		err = w.controller.Flush()
	}
	return n, err
}

func splitProof(token, id, method string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = io.WriteString(mac, method+"\x00"+id)
	return hex.EncodeToString(mac.Sum(nil))
}

func dialSplitHTTP(ctx context.Context, dial *net.Dialer, addr string, c config.BackhaulConfig, t *tls.Config) (net.Conn, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(nonce[:])
	url := "http://" + addr + strings.TrimSuffix(requestPath(c), "/") + "/" + id
	transport := &http.Transport{
		DialContext:           dial.DialContext,
		ResponseHeaderTimeout: linkTimeout,
		DisableCompression:    true,
		DisableKeepAlives:     true,
	}
	ctx, cancel := context.WithCancel(ctx)
	download, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	download.Header.Set("X-Tunnel-Session", splitProof(c.Token, id, download.Method))
	resp, err := transport.RoundTrip(download)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		return nil, errors.New("peer refused Split HTTP download")
	}
	end, wire := net.Pipe()
	upload, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, wire)
	upload.Header.Set("X-Tunnel-Session", splitProof(c.Token, id, upload.Method))
	go func() {
		defer wire.Close()
		defer cancel()
		res, err := transport.RoundTrip(upload)
		if err == nil {
			res.Body.Close()
		}
	}()
	go func() {
		defer wire.Close()
		defer resp.Body.Close()
		defer cancel()
		_, _ = io.Copy(wire, resp.Body)
	}()
	lane := tls.Client(&splitLane{Conn: end, local: end.LocalAddr(), remote: httpPeer(addr)}, t)
	hctx, stop := context.WithTimeout(ctx, linkTimeout)
	defer stop()
	if err := lane.HandshakeContext(hctx); err != nil {
		cancel()
		end.Close()
		return nil, err
	}
	return lane, nil
}
