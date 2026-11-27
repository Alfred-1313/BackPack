package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/daggerconnect"
)

const ctDaggerPrefix = "dagger-"
const ctDaggerTUNPrefix = ctDaggerPrefix + "tun-"

var ctDaggerTUNProfiles = []string{"tcp", "udp", "icmp", "gre", "ipip", "bip"}

func ctDaggerCases() []*connTestCase {
	var cases []*connTestCase
	for _, option := range DaggerOptions() {
		if option.Value == "tun" {
			for _, profile := range ctDaggerTUNProfiles {
				cases = append(cases, &connTestCase{kind: option.Direction, tr: ctDaggerTUNPrefix + profile, extra: true})
			}
			continue
		}
		cases = append(cases, &connTestCase{kind: option.Direction, tr: ctDaggerPrefix + option.Value, extra: true})
	}
	return cases
}

func ctDaggerLabel(mode string) string {
	if profile, ok := strings.CutPrefix(mode, ctDaggerTUNPrefix); ok {
		return "TUN / " + strings.ToUpper(profile)
	}
	flavor, ok := strings.CutPrefix(mode, ctDaggerPrefix)
	if ok {
		for _, option := range daggerconnect.Options() {
			if option.Value == flavor {
				return option.Label
			}
		}
	}
	return ""
}

func (s *ConnTestIran) daggerSkip(mode string) string {
	if s.daggerLicense == "" {
		return "DaggerConnect license PSK was not provided"
	}
	v6 := ctFamily(s.link.Host) == "IPv6"
	if mode == ctDaggerPrefix+"dc6" {
		if !v6 {
			return "DC6 needs an IPv6 test path"
		}
		if net.ParseIP(s.daggerIPv4).To4() == nil {
			return "DC6 also needs Iran's licensed IPv4 address"
		}
	} else if v6 {
		return "this DaggerConnect transport requires an IPv4 path"
	}
	return ""
}

type ctHelperProcess struct {
	dir, label, secret string
	engine             *ctEngine
}

func (p *ctHelperProcess) startupFailure() (bool, string) {
	exited, detail := p.engine.exited()
	if !exited {
		return false, ""
	}
	if detail == "" {
		return true, p.label + " exited before carrying traffic"
	}
	detail = strings.ReplaceAll(detail, p.secret, "[private]")
	return true, p.label + " exited: " + ctExtraError(errors.New(detail))
}

func (p *ctHelperProcess) close() {
	if p.engine != nil {
		ctStopAll([]*ctEngine{p.engine})
		// The launcher can start the licensed core as a child. Reap its
		// process group even if the Go wrapper has already returned.
		_ = syscall.Kill(-p.engine.cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = os.RemoveAll(p.dir)
}

type ctDaggerJob struct {
	Config config.DaggerConfig
	CA     string
}

// All launches in this tester share the same verified helper installation.
var ctDaggerEnsure sync.Mutex

func (p *ctHelperProcess) start(ctx context.Context, name string, job ctDaggerJob) error {
	c := job.Config
	if _, err := daggerconnect.Build(name, c); err != nil {
		return err
	}
	ctDaggerEnsure.Lock()
	err := daggerconnect.Ensure(ctx)
	ctDaggerEnsure.Unlock()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.label, p.secret = "DaggerConnect", c.License
	env := []string{"TMPDIR=" + p.dir}
	if job.CA != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(job.CA)) {
			return errors.New("invalid Dagger HTTPS test certificate")
		}
		rootsDir := filepath.Join(p.dir, "trust")
		if err := os.Mkdir(rootsDir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(rootsDir, "test.pem"), []byte(job.CA), 0600); err != nil {
			return err
		}
		systemDirs := os.Getenv("SSL_CERT_DIR")
		if systemDirs == "" {
			systemDirs = "/etc/ssl/certs:/etc/pki/tls/certs"
		}
		env = append(env, "SSL_CERT_DIR="+systemDirs+":"+rootsDir)
	}
	var doc bytes.Buffer
	if err := toml.NewEncoder(&doc).Encode(struct {
		Dagger config.DaggerConfig `toml:"dagger"`
	}{Dagger: c}); err != nil {
		return err
	}
	p.engine, err = startCTEngine(p.dir, name, doc.String(), env...)
	return err
}

// TUN profiles use different address pairs to keep their traffic and their
// interfaces separate. Neither endpoint starts over an existing subnet.
func ctDaggerTUNPair(name string) (string, string) {
	sum := sha256.Sum256([]byte(name))
	base := [4]byte{198, 18 + (sum[0] & 1), sum[1], sum[2] & 0xfc}
	base[3]++
	near := netip.AddrFrom4(base).String()
	base[3]++
	return near, netip.AddrFrom4(base).String()
}

func ctDaggerTUNAvailable(address string) error {
	endpoint := netip.MustParseAddr(address)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err == nil && prefix.Contains(endpoint) {
			return errors.New("temporary Dagger TUN subnet is already in use")
		}
	}
	return nil
}

func (s *ConnTestIran) setupDagger(ctx context.Context, c *connTestCase, mode string) error {
	flavor := strings.TrimPrefix(mode, ctDaggerPrefix)
	cfg := config.DaggerConfig{
		Role: "server", Flavor: flavor, IranIP: s.link.Host, License: s.daggerLicense,
		HTTPHost: "conntest.backpack.invalid", HTTPPath: "/bp-conntest",
	}
	if flavor == "dc6" {
		cfg.IranIP, cfg.IranIPv6 = s.daggerIPv4, s.link.Host
	}
	s.mu.Lock()
	outer := ctPickPort(s.helperPorts, true)
	if c.kind == "reverse" {
		c.entry = ctPickPort(s.helperPorts, false)
	}
	s.mu.Unlock()
	if outer == 0 || (c.kind == "reverse" && c.entry == 0) {
		return errors.New("no free local port for the DaggerConnect test")
	}
	cfg.Port = uint16(outer)

	if profile, ok := strings.CutPrefix(mode, ctDaggerTUNPrefix); ok {
		cfg.Flavor, cfg.TUNProfile, cfg.TUNPort = "tun", profile, cfg.Port
		cfg.HeartbeatSeconds, _ = setupTuningValue("dagger-tun", SetupTuning{Preset: s.link.Preset})
		cfg.KharejIP = ctIPv4Context(ctx, s.Kharej())
		cfg.IranTunnelIP, cfg.KharejTunnelIP = ctDaggerTUNPair(c.name)
		if err := ctDaggerTUNAvailable(cfg.IranTunnelIP); err != nil {
			return err
		}
		c.peerIP = cfg.KharejTunnelIP
	} else {
		if value, err := setupTuningValue(mode, SetupTuning{Preset: s.link.Preset}); err == nil {
			cfg.ConnectionPool = value
		}
		cfg.LoopbackOnly = true
		cfg.Ports = fmt.Sprintf("tcp:%d=%d", c.entry, s.link.TCP)
	}
	dir, err := os.MkdirTemp(s.dir, "dagger-")
	if err != nil {
		return err
	}
	p := &ctHelperProcess{dir: dir}
	c.helper, c.localStarted = p, true
	peer := ctDaggerJob{Config: cfg}
	peer.Config.Role, peer.Config.Ports, peer.Config.LoopbackOnly = "client", "", false
	if daggerconnect.UsesTLSFiles(flavor) {
		cfg.CertFile, cfg.KeyFile, peer.CA, err = ctCertificate(dir, c.name, cfg.IranIP)
		if err != nil {
			return err
		}
	}
	if err := p.start(ctx, c.name, ctDaggerJob{Config: cfg}); err != nil {
		return err
	}
	body, err := json.Marshal(peer)
	if err != nil {
		return err
	}
	c.peerStarted = true
	_, err = s.coord.extra.exchange(ctx, c.extraWorker, ctExtraJob{
		Action: "join", Mode: mode, Name: c.name, Token: base64.RawURLEncoding.EncodeToString(body),
	})
	if err == nil {
		if exited, detail := p.startupFailure(); exited {
			err = errors.New(detail)
		}
	}
	return err
}

func (w *ctExtraInstance) joinDagger(ctx context.Context, link ConnTestLink, job ctExtraJob) error {
	var peer ctDaggerJob
	data, err := base64.RawURLEncoding.DecodeString(job.Token)
	if err != nil || json.Unmarshal(data, &peer) != nil {
		return errors.New("invalid DaggerConnect test setup")
	}
	c := peer.Config
	if c.Role != "client" || c.Ports != "" || c.LoopbackOnly || c.CertFile != "" || c.KeyFile != "" ||
		daggerconnect.UsesTLSFiles(c.Flavor) != (peer.CA != "") || !ctDaggerTestEndpoint(link.Host, job, c) {
		return errors.New("unexpected DaggerConnect test settings")
	}
	if c.Flavor == "tun" {
		if err := ctDaggerTUNAvailable(c.KharejTunnelIP); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp(w.dir, "dagger-")
	if err != nil {
		return err
	}
	w.name, w.mode, w.helper = job.Name, job.Mode, &ctHelperProcess{dir: dir}
	if err := w.helper.start(ctx, job.Name, peer); err != nil {
		return err
	}
	if c.Flavor != "tun" {
		ctSleep(ctx, time.Second)
		if err := ctx.Err(); err != nil {
			return err
		}
		if exited, detail := w.helper.startupFailure(); exited {
			return errors.New(detail)
		}
		return nil
	}
	// The customary TCP echo listener for forwarded transports is already
	// running. TUN traffic must use the address assigned by the real core.
	echoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	address := net.JoinHostPort(c.KharejTunnelIP, fmt.Sprint(link.L3))
	var listener net.Listener
	for echoCtx.Err() == nil {
		if listener, err = net.Listen("tcp4", address); err == nil {
			w.echo = &ctEchoes{}
			w.echo.add(listener)
			go ctServeTCPEcho(listener, w.echo)
			return nil
		}
		if exited, detail := w.helper.startupFailure(); exited {
			return errors.New(detail)
		}
		ctSleep(echoCtx, 250*time.Millisecond)
	}
	return errors.New("DaggerConnect did not bring up the Kharej TUN interface")
}

func ctDaggerTestEndpoint(host string, job ctExtraJob, c config.DaggerConfig) bool {
	if c.Flavor == "dc6" {
		return job.Mode == ctDaggerPrefix+"dc6" && c.IranIPv6 == host && ctFamily(host) == "IPv6"
	}
	if c.IranIP != host || ctFamily(host) != "IPv4" {
		return false
	}
	if c.Flavor == "tun" {
		near, far := ctDaggerTUNPair(job.Name)
		return job.Mode == ctDaggerTUNPrefix+c.TUNProfile && c.IranTunnelIP == near && c.KharejTunnelIP == far
	}
	return job.Mode == ctDaggerPrefix+c.Flavor && ctDaggerLabel(job.Mode) != ""
}
