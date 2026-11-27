package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/pengutunnel"
)

func ctPenguMode(id string) (pengutunnel.Mode, string, bool) {
	suffix, prefixed := strings.CutPrefix(id, pengutunnel.Prefix)
	direction, id, _ := strings.Cut(suffix, "-")
	mode, found := pengutunnel.Find(id)
	ok := prefixed && found && (direction == "direct" || direction == "reverse" && !mode.IPX)
	return mode, direction, ok
}

func ctPenguCases() []*connTestCase {
	var cases []*connTestCase
	for _, mode := range pengutunnel.Options() {
		if !mode.IPX {
			cases = append(cases, &connTestCase{kind: "reverse", tr: pengutunnel.Prefix + "reverse-" + mode.ID, extra: true})
		}
		cases = append(cases, &connTestCase{kind: "direct", tr: pengutunnel.Prefix + "direct-" + mode.ID, extra: true})
	}
	return cases
}

func (p *ctHelperProcess) startPengu(ctx context.Context, name string, cfg config.PenguConfig) error {
	if err := checkPenguLocal(cfg); err != nil {
		return err
	}
	if err := pengutunnel.Ensure(ctx); err != nil {
		return err
	}
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(struct {
		Pengu config.PenguConfig `toml:"pengu"`
	}{cfg}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.label, p.secret = pengutunnel.Label, cfg.Key
	var err error
	p.engine, err = startCTEngine(p.dir, name, body.String(), "TMPDIR="+p.dir)
	return err
}

func (s *ConnTestIran) setupPengu(ctx context.Context, c *connTestCase, mode pengutunnel.Mode, direction string) error {
	key, err := backhaulToken()
	if err != nil {
		return err
	}
	cfg := config.PenguConfig{Mode: mode.ID, Direction: direction, Side: "iran", IranHost: s.link.Host,
		KharejHost: s.Kharej(), Family: strings.ToLower(ctFamily(s.link.Host)), Key: key, Preset: s.link.Preset}
	penguDefaults(&cfg, mode)
	if mode.IPX {
		cfg.IranHost = ctIPv4Context(ctx, cfg.IranHost)
		cfg.KharejHost = ctIPv4Context(ctx, cfg.KharejHost)
	}
	ctrl := s.coord.extra
	job := ctExtraJob{Action: "port", Mode: c.tr, Name: c.name}
	if direction == "direct" && !mode.IPX {
		res, err := ctrl.exchange(ctx, c.extraWorker, job)
		if err != nil {
			return err
		}
		if res.Port < 1 || res.Port > 65535 {
			return errors.New("invalid PenguTunnel peer port")
		}
		cfg.Port = uint16(res.Port)
	}
	s.mu.Lock()
	if direction == "reverse" {
		cfg.Port = uint16(ctPickPort(s.helperPorts, true))
	}
	// Only the port the carrier listens on and the health port need to be
	// distinct. Keep the same health port on the far side for status.
	cfg.HealthPort = uint16(ctPickPort(s.helperPorts, false))
	for cfg.HealthPort != 0 && cfg.HealthPort == cfg.Port {
		cfg.HealthPort = uint16(ctPickPort(s.helperPorts, false))
	}
	if !mode.TUN {
		c.entry = ctPickPort(s.helperPorts, false)
	}
	s.mu.Unlock()
	if mode.TUN {
		cfg.IranTunnel, cfg.KharejTunnel = ctBackhaulPair(c.name)
		far, _ := netip.ParsePrefix(cfg.KharejTunnel)
		c.peerIP = far.Addr().String()
	} else {
		if c.entry == 0 {
			return errors.New("no free PenguTunnel forwarding port")
		}
		cfg.Ports = []string{fmt.Sprintf("127.0.0.1:%d=127.0.0.1:%d", c.entry, s.link.TCP)}
	}
	dir, err := os.MkdirTemp(s.dir, "pengu-")
	if err != nil {
		return err
	}
	process := &ctHelperProcess{dir: dir}
	c.helper, c.localStarted = process, true
	if err := process.startPengu(ctx, c.name, cfg); err != nil {
		return err
	}
	job.Action = "join"
	job.Token, err = penguShare(c.name, cfg).Encode()
	if err != nil {
		return err
	}
	c.peerStarted = true
	if _, err := ctrl.exchange(ctx, c.extraWorker, job); err != nil {
		return err
	}
	return ctBackhaulStartupCheck(ctx, process)
}

func (w *ctExtraInstance) joinPengu(ctx context.Context, test ConnTestLink, job ctExtraJob, mode pengutunnel.Mode, direction string) error {
	l, err := DecodeShareLink(job.Token)
	if err != nil || l.Name != job.Name || l.Tr != pengutunnel.Prefix+mode.ID ||
		l.Kind != direction || l.From != "iran" || l.HealthPort == 0 || l.HealthPort > 65535 || l.Ports != "" {
		return errors.New("invalid PenguTunnel test setup link")
	}
	iran := test.Host
	if mode.IPX {
		iran = ctIPv4Context(ctx, iran)
	}
	if iran == "" || l.Host != iran {
		return errors.New("unexpected PenguTunnel test endpoint")
	}
	var port uint16
	if !mode.IPX {
		number, err := strconv.ParseUint(l.Port, 10, 16)
		if err != nil || number == 0 {
			return errors.New("invalid PenguTunnel test port")
		}
		port = uint16(number)
	} else if l.Port != "" {
		return errors.New("PenguTunnel IPX uses raw IP")
	}
	cfg := penguPeer(l, mode.ID, port)
	if mode.TUN {
		near, far := ctBackhaulPair(job.Name)
		if cfg.IranTunnel != near || cfg.KharejTunnel != far {
			return errors.New("unexpected PenguTunnel test subnet")
		}
	}
	dir, err := os.MkdirTemp(w.dir, "pengu-")
	if err != nil {
		return err
	}
	w.name, w.mode, w.helper = job.Name, job.Mode, &ctHelperProcess{dir: dir}
	if err := w.helper.startPengu(ctx, job.Name, cfg); err != nil {
		return err
	}
	if !mode.TUN {
		return ctBackhaulStartupCheck(ctx, w.helper)
	}
	far, _ := netip.ParsePrefix(cfg.KharejTunnel)
	addr := net.JoinHostPort(far.Addr().String(), strconv.Itoa(test.L3))
	window, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for window.Err() == nil {
		if l, err := net.Listen("tcp4", addr); err == nil {
			w.echo = &ctEchoes{}
			w.echo.add(l)
			go ctServeTCPEcho(l, w.echo)
			return ctBackhaulStartupCheck(ctx, w.helper)
		}
		if failed, detail := w.helper.startupFailure(); failed {
			return errors.New(detail)
		}
		ctSleep(window, 250*time.Millisecond)
	}
	return errors.New("PenguTunnel did not bring up the Kharej TUN interface")
}
