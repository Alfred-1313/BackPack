package manage

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/omni"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

const peerJoinScheme = shareScheme + "p."
const peerJoinWindow = 15 * time.Minute

var peerJoinSlots = make(chan struct{}, 8)

type peerJoinLink struct {
	Addr, Key, Pin string
}

type peerJoinResult struct {
	Link, Driver, Token, Mode, Name string
	MuxToken                        string
}

// beginPeerJoin lets the Kharej operator supply the peer via an authenticated
// connection. Only that connection's source address is used for setup.
func beginPeerJoin(parent context.Context, name, kind string, allowIPv6 bool, setup func(context.Context, string, string) (peerJoinResult, error)) (SetupLinkInfo, error) {
	host := linkHost()
	if net.ParseIP(host).To4() == nil && allowIPv6 {
		host = PublicIPv6()
	}
	if net.ParseIP(host) == nil && allowIPv6 {
		return SetupLinkInfo{}, errors.New("could not detect an address on Iran for the Setup command")
	}
	if net.ParseIP(host).To4() == nil && !allowIPv6 {
		return SetupLinkInfo{}, errors.New("could not detect an IPv4 address on Iran for the Setup command")
	}
	select {
	case peerJoinSlots <- struct{}{}:
	default:
		return SetupLinkInfo{}, errors.New("too many pending Setup commands; try again when one expires")
	}
	info, err := startPeerJoin(parent, host, name, kind, setup, func() { <-peerJoinSlots })
	if err != nil {
		<-peerJoinSlots
	}
	return info, err
}

func startPeerJoin(parent context.Context, host, name, kind string, setup func(context.Context, string, string) (peerJoinResult, error), finished func()) (SetupLinkInfo, error) {
	if parent == nil {
		parent = context.Background()
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return SetupLinkInfo{}, errors.New("invalid Iran address for the Setup command")
	}
	key, err := backhaulToken()
	if err != nil {
		return SetupLinkInfo{}, err
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SetupLinkInfo{}, err
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(peerJoinWindow),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, private)
	if err != nil {
		return SetupLinkInfo{}, err
	}
	network := "tcp4"
	if net.ParseIP(host).To4() == nil {
		network = "tcp6"
	}
	listener, err := net.Listen(network, ":0")
	if err != nil {
		return SetupLinkInfo{}, err
	}
	ctx, stop := context.WithTimeout(parent, peerJoinWindow)
	sum := sha256.Sum256(der)
	addr := netip.AddrPortFrom(ip.Unmap(), uint16(listener.Addr().(*net.TCPAddr).Port))
	keyBytes, _ := base64.StdEncoding.DecodeString(key)
	payload := append([]byte{1}, keyBytes...)
	payload = append(payload, sum[:]...)
	payload = binary.BigEndian.AppendUint16(payload, addr.Port())
	payload = append(payload, addr.Addr().AsSlice()...)
	raw := peerJoinScheme + base64.RawURLEncoding.EncodeToString(payload)
	var mu sync.Mutex
	var peer string
	var result peerJoinResult
	var lastError error
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      85 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    4096,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodPost || r.URL.Path != "/join" || r.ContentLength != 0 ||
				subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
				http.NotFound(w, r)
				return
			}
			remote, _, err := net.SplitHostPort(r.RemoteAddr)
			address, parseErr := netip.ParseAddr(remote)
			if err != nil || parseErr != nil ||
				(!address.IsGlobalUnicast() && !address.IsLoopback()) {
				http.Error(w, "could not determine the peer's address", http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if peer == "" {
				peer = address.Unmap().String()
				applyCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
				result, lastError = setup(applyCtx, host, peer)
				cancel()
				// A lost response can be retried from this peer for a short period.
				time.AfterFunc(time.Minute, stop)
			}
			if peer != address.Unmap().String() {
				http.Error(w, "this Setup command is already paired", http.StatusConflict)
				return
			}
			if lastError != nil {
				http.Error(w, lastError.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		}),
	}
	server.SetKeepAlivesEnabled(false)
	go func() {
		defer finished()
		defer stop()
		closeOnStop := context.AfterFunc(ctx, func() { server.Close() })
		defer closeOnStop()
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
		wrapped := tls.NewListener(listener, tlsConfig)
		defer wrapped.Close()
		_ = server.Serve(wrapped)
	}()
	return SetupLinkInfo{Name: name, For: "kharej", Kind: kind, Apply: "sudo backpack link apply '" + raw + "'",
		Install: app.SourceInstallCommand("link", "apply", raw), Where: "Kharej terminal", Pending: true}, nil
}

func decodePeerJoin(raw string) (peerJoinLink, error) {
	var link peerJoinLink
	encoded := strings.TrimPrefix(raw, peerJoinScheme)
	if len(encoded) <= 2048 {
		body, err := base64.RawURLEncoding.DecodeString(encoded)
		if err == nil && len(body) != 0 {
			switch body[0] {
			case 1:
				if len(body) == 71 || len(body) == 83 {
					ip, _ := netip.AddrFromSlice(body[67:])
					return peerJoinLink{
						Key:  base64.StdEncoding.EncodeToString(body[1:33]),
						Pin:  base64.RawURLEncoding.EncodeToString(body[33:65]),
						Addr: netip.AddrPortFrom(ip, binary.BigEndian.Uint16(body[65:67])).String(),
					}, nil
				}
			case '{':
				if err := json.Unmarshal(body, &link); err == nil {
					return link, nil
				}
			}
		}
	}
	return peerJoinLink{}, errors.New("invalid peer Setup command")
}

func fetchPeerJoin(raw string) (peerJoinResult, error) {
	link, err := decodePeerJoin(raw)
	if err != nil {
		return peerJoinResult{}, err
	}
	addr, err := netip.ParseAddrPort(link.Addr)
	pin, pinErr := base64.RawURLEncoding.DecodeString(link.Pin)
	key, keyErr := base64.StdEncoding.DecodeString(link.Key)
	if err != nil || addr.Addr().Zone() != "" || addr.Port() == 0 || pinErr != nil || len(pin) != sha256.Size ||
		keyErr != nil || len(key) != 32 {
		return peerJoinResult{}, errors.New("invalid peer Setup address or credential")
	}
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	tr := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 8 * time.Second, TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // Trust only the certificate pinned by the Setup command.
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 0 {
				actual := sha256.Sum256(state.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(actual[:], pin) == 1 {
					return nil
				}
			}
			return errors.New("the Setup certificate does not match")
		},
	}}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr.String()+"/join", nil)
	if err != nil {
		return peerJoinResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+link.Key)
	res, err := tr.RoundTrip(req)
	if err != nil {
		return peerJoinResult{}, fmt.Errorf("could not reach Iran for Setup: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return peerJoinResult{}, fmt.Errorf("Iran rejected Setup: %s", strings.TrimSpace(string(msg)))
	}
	var out peerJoinResult
	err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out)
	return out, err
}

// StartDirectPeerSetup defers the two-address configurations until Kharej
// checks in. Both ends are rendered by the regular Direct setup code.
func StartDirectPeerSetup(ctx context.Context, form NewDirectTunnel) (SetupLinkInfo, error) {
	switch form.Carrier {
	case l3.CarrierGRETAP, l3.CarrierL2TPv3, l3.CarrierEasyTier:
	default:
		return SetupLinkInfo{}, errors.New("this transport does not use automatic peer setup")
	}
	if form.Side != "iran" {
		return SetupLinkInfo{}, errors.New("create the Setup command on Iran")
	}
	if form.Carrier == l3.CarrierEasyTier && form.Token == "" {
		form.Token = randomToken(64)
	}
	preview := form
	form.PeerAddr = strings.TrimSpace(form.PeerAddr)
	preview.PeerAddr = form.PeerAddr
	if preview.PeerAddr == "" {
		preview.PeerAddr = "192.0.2.1"
	}
	spec, err := preview.spec()
	if err != nil {
		return SetupLinkInfo{}, err
	}
	form.Name = spec.Name
	return beginPeerJoin(ctx, form.Name, "direct", form.Carrier == l3.CarrierEasyTier, func(_ context.Context, host, peer string) (peerJoinResult, error) {
		if form.PeerAddr == "" {
			form.PeerAddr = peer
		}
		service, _, err := CreateDirectTunnel(form)
		if err != nil {
			return peerJoinResult{}, err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(service, app.ServicePrefix), ".service")
		info, err := SetupLinkFor(name, host)
		return peerJoinResult{Link: info.Link}, err
	})
}

func applyPeerJoin(raw string, options LinkApplyOptions) (LinkApplied, error) {
	result, err := fetchPeerJoin(raw)
	if err != nil {
		return LinkApplied{}, err
	}
	if result.Link != "" {
		link, err := DecodeShareLink(result.Link)
		if err != nil {
			return LinkApplied{}, err
		}
		return applyDirectLink(link, options)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	done := LinkApplied{Name: result.Name, Transport: result.Mode, Kind: "direct", External: true}
	switch result.Driver {
	case "gost":
		out, err := gostdirect.Install(ctx, gostdirect.Form{Action: "join", Token: result.Token})
		done.Service, done.Active = out.Unit, err == nil
		return done, err
	case "omni":
		if result.Mode == "reverse-mux" {
			done.Kind = "reverse"
		}
		var output bytes.Buffer
		err := omni.RunContext(ctx, []string{"join", result.Mode}, strings.NewReader(result.Token), &output, &output)
		if err == nil && result.MuxToken != "" {
			_, err = ctOmniRun(ctx, result.MuxToken, "mux-join")
			if err != nil {
				return done, err
			}
		}
		done.Service = "backpack-omni-" + result.Name + ".service"
		done.Transport = omni.Label(result.Mode)
		done.Active = err == nil && IsActive(done.Service)
		if err != nil && output.Len() != 0 {
			err = errors.New(ctExtraError(errors.New(output.String())))
		}
		if fields, fieldErr := ctOmniTokenFields(result.Token); fieldErr == nil && !strings.HasPrefix(result.Mode, "hysteria") {
			done.AwaitPeer = routedPeerProbe(fields[6], fields[7])
		}
		return done, err
	default:
		return LinkApplied{}, errors.New("invalid peer Setup response")
	}
}

// routedPeerProbe sends its echo inside the tunnel, bound to the local tunnel
// address. The public peer answering ICMP cannot satisfy this check.
func routedPeerProbe(local, peer string) func(time.Duration) (bool, string) {
	if net.ParseIP(local).To4() == nil || net.ParseIP(peer).To4() == nil || local == peer {
		return nil
	}
	return func(within time.Duration) (bool, string) {
		ctx, cancel := context.WithTimeout(context.Background(), within)
		defer cancel()
		for ctx.Err() == nil {
			if err := exec.CommandContext(ctx, "ping", "-I", local, "-c", "1", "-W", "1", peer).Run(); err == nil {
				return true, "peer answered across the tunnel"
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		return false, "no reply across the tunnel"
	}
}
