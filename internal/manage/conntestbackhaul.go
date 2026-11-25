package manage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/nativebackhaul"
)

const ctBackhaulPrefix = "native-"

func ctBackhaulCases() []*connTestCase {
	var cases []*connTestCase
	for _, item := range nativebackhaul.Options() {
		cases = append(cases, &connTestCase{kind: item.Direction, tr: ctBackhaulPrefix + item.Value, extra: true})
	}
	return cases
}

func ctBackhaulLabel(tr string) string {
	item, ok := nativebackhaul.Find(strings.TrimPrefix(tr, ctBackhaulPrefix))
	if !ok {
		return tr
	}
	return item.Label
}

func backhaulTUNAvailable(address string) error {
	prefix, err := netip.ParsePrefix(address)
	if err != nil {
		return err
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, raw := range addrs {
		existing, err := netip.ParsePrefix(raw.String())
		if err == nil && (existing.Contains(prefix.Addr()) || prefix.Contains(existing.Addr())) {
			return fmt.Errorf("Backhaul TUN subnet %s overlaps an existing interface", prefix.Masked())
		}
	}
	return nil
}

func ctBackhaulPair(name string) (string, string) {
	near, far := ctDaggerTUNPair(name)
	return near + "/30", far + "/30"
}

func (p *ctHelperProcess) startBackhaul(ctx context.Context, name string, c config.BackhaulConfig) error {
	if err := nativebackhaul.Validate(c); err != nil {
		return err
	}
	var doc bytes.Buffer
	if err := toml.NewEncoder(&doc).Encode(struct {
		Backhaul config.BackhaulConfig `toml:"backhaul"`
	}{Backhaul: c}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.label, p.secret = "BackPack", c.Token
	var err error
	p.engine, err = startCTEngine(p.dir, name, doc.String())
	return err
}

func (s *ConnTestIran) setupBackhaul(ctx context.Context, c *connTestCase, tr string) error {
	mode := strings.TrimPrefix(tr, ctBackhaulPrefix)
	tun := backhaulpremium.IsTUN(mode)
	token, err := backhaulToken()
	if err != nil {
		return err
	}
	cfg := config.BackhaulConfig{Native: true, Role: "server", Mode: mode, IranHost: s.link.Host, Token: token}
	s.mu.Lock()
	cfg.Port = uint16(ctPickPort(s.helperPorts, true))
	if !tun {
		c.entry = ctPickPort(s.helperPorts, false)
	}
	s.mu.Unlock()
	if tun {
		cfg.IranTunnel, cfg.KharejTunnel = ctBackhaulPair(c.name)
		if err := backhaulTUNAvailable(cfg.IranTunnel); err != nil {
			return err
		}
		far, _ := netip.ParsePrefix(cfg.KharejTunnel)
		c.peerIP = far.Addr().String()
	} else {
		if c.entry == 0 {
			return errors.New("no free tunnel forwarding port")
		}
		cfg.Ports = []string{fmt.Sprintf("127.0.0.1:%d=127.0.0.1:%d", c.entry, s.link.TCP)}
	}
	dir, err := os.MkdirTemp(s.dir, "backhaul-")
	if err != nil {
		return err
	}
	process := &ctHelperProcess{dir: dir}
	c.helper, c.localStarted = process, true
	if err := process.startBackhaul(ctx, c.name, cfg); err != nil {
		return err
	}
	c.peerStarted = true
	_, err = s.coord.extra.exchange(ctx, c.extraWorker, ctExtraJob{
		Action: "join", Mode: tr, Name: c.name, Token: backhaulPeerToken(c.name, cfg),
	})
	if err == nil {
		if exited, detail := process.startupFailure(); exited {
			err = errors.New(detail)
		}
	}
	return err
}

func (w *ctExtraInstance) joinBackhaul(ctx context.Context, link ConnTestLink, job ctExtraJob) error {
	var peer backhaulLink
	data, err := base64.RawURLEncoding.DecodeString(job.Token)
	if err != nil || json.Unmarshal(data, &peer) != nil || peer.Name != job.Name {
		return errors.New("invalid Backhaul test setup")
	}
	c := peer.Side
	mode := strings.TrimPrefix(job.Mode, ctBackhaulPrefix)
	if !c.Native || c.Role != "client" || c.Mode != mode || c.IranHost != link.Host || len(c.Ports) != 0 {
		return errors.New("unexpected Backhaul test configuration")
	}
	if err := nativebackhaul.Validate(c); err != nil {
		return err
	}
	if backhaulpremium.IsTUN(mode) {
		near, far := ctBackhaulPair(job.Name)
		if c.IranTunnel != near || c.KharejTunnel != far {
			return errors.New("unexpected Backhaul test subnet")
		}
		if err := backhaulTUNAvailable(far); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp("", "backpack-ct-backhaul-")
	if err != nil {
		return err
	}
	w.name, w.mode, w.helper = job.Name, job.Mode, &ctHelperProcess{dir: dir}
	if err := w.helper.startBackhaul(ctx, job.Name, c); err != nil {
		return err
	}
	if !backhaulpremium.IsTUN(mode) {
		return ctBackhaulStartupCheck(ctx, w.helper)
	}
	far, _ := netip.ParsePrefix(c.KharejTunnel)
	address := net.JoinHostPort(far.Addr().String(), fmt.Sprint(link.L3))
	echoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for echoCtx.Err() == nil {
		if l, err := net.Listen("tcp4", address); err == nil {
			w.echo = &ctEchoes{}
			w.echo.add(l)
			go ctServeTCPEcho(l, w.echo)
			return ctBackhaulStartupCheck(ctx, w.helper)
		}
		if exited, detail := w.helper.startupFailure(); exited {
			return errors.New(detail)
		}
		ctSleep(echoCtx, 250*time.Millisecond)
	}
	return errors.New("BackPack did not bring up the Kharej TUN interface")
}

func ctBackhaulStartupCheck(ctx context.Context, process *ctHelperProcess) error {
	window, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for window.Err() == nil {
		if failed, detail := process.startupFailure(); failed {
			return errors.New(detail)
		}
		ctSleep(window, 200*time.Millisecond)
	}
	return ctx.Err()
}
