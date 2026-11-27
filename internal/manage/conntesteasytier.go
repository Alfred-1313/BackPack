package manage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/easytier"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

const ctEasyTierPrefix = "easytier-"

func ctEasyTierMode(id string) (direction, scheme string, ok bool) {
	value, found := strings.CutPrefix(id, ctEasyTierPrefix)
	if !found {
		return "", "", false
	}
	direction, scheme, ok = strings.Cut(value, "-")
	if !ok || (direction != "direct" && direction != "reverse") || scheme == "" {
		return "", "", false
	}
	normalized, err := easytier.Protocol(scheme)
	return direction, scheme, err == nil && normalized == scheme
}

func ctEasyTierCases() []*connTestCase {
	var cases []*connTestCase
	for _, direction := range []string{"direct", "reverse"} {
		for _, scheme := range []string{"tcp", "udp", "ws", "wss"} {
			// The ordinary Direct suite already tests EasyTier TCP.
			if direction == "direct" && scheme == "tcp" {
				continue
			}
			cases = append(cases, &connTestCase{kind: direction, tr: ctEasyTierPrefix + direction + "-" + scheme, extra: true})
		}
	}
	return cases
}

func ctEasyTierSpec(link ConnTestLink, name, mode, dialHost string, port int, side directSide) (l3Spec, error) {
	direction, scheme, ok := ctEasyTierMode(mode)
	if !ok || port < 1 || port > 65535 || net.ParseIP(dialHost) == nil {
		return l3Spec{}, errors.New("invalid EasyTier test endpoint")
	}
	reverse := direction == "reverse"
	sum := sha256.Sum256([]byte(name))
	block := netip.AddrFrom4([4]byte{10, 224 + sum[0]%8, sum[1], sum[2] & 252})
	local, peer := block.Next(), block.Next().Next()
	if side == sideKharej {
		local, peer = peer, local
	}
	host := dialHost
	if (side == sideIran) == reverse {
		host = ctBindHost(dialHost)
	}
	spec := l3Spec{
		RoutedLinkSettings: config.RoutedLinkSettings{
			EasyTierNetwork: name, EasyTierProtocol: scheme, EasyTierReverse: reverse,
		},
		Name: name, Side: side, Carrier: l3.CarrierEasyTier,
		Addr: net.JoinHostPort(host, strconv.Itoa(port)), Token: ctCaseToken(link.Tok, direction, mode),
		Iface: "bpe" + hex.EncodeToString(sum[:6]), LocalIP: netip.PrefixFrom(local, 30).String(),
		PeerIP: peer.String(), MTU: defaultL3MTU,
	}
	findL3Preset(link.Preset).apply(&spec)
	return spec, nil
}

func (p *ctHelperProcess) startEasyTier(root string, spec l3Spec) error {
	if _, err := easytier.Binary(); err != nil {
		return err
	}
	if owner := l3BlockOwner(spec.LocalIP); owner != "" {
		return fmt.Errorf("temporary EasyTier subnet overlaps %s", owner)
	}
	name, body, err := directBodyFromSpec(spec)
	if err != nil {
		return err
	}
	p.dir, err = os.MkdirTemp(root, "easytier-")
	if err != nil {
		return err
	}
	p.label, p.secret = "EasyTier", spec.Token
	p.engine, err = startCTEngine(p.dir, name, body, "TMPDIR="+p.dir)
	return err
}

func (s *ConnTestIran) setupEasyTier(ctx context.Context, c *connTestCase, ctrl *ctExtraControl) error {
	job := ctExtraJob{Action: "port", Name: c.name, Mode: c.tr}
	host := s.Kharej()
	var port int
	if c.kind == "reverse" {
		host = s.link.Host
		s.mu.Lock()
		port = ctPickPort(s.helperPorts, true)
		s.mu.Unlock()
	} else {
		outer, err := ctrl.exchange(ctx, c.extraWorker, job)
		if err != nil {
			return err
		}
		port = outer.Port
	}
	spec, err := ctEasyTierSpec(s.link, c.name, c.tr, host, port, sideIran)
	if err != nil {
		return err
	}
	c.helper, c.localStarted = &ctHelperProcess{}, true
	if err := c.helper.startEasyTier(s.dir, spec); err != nil {
		return err
	}
	c.peerIP, c.peerStarted = spec.PeerIP, true
	job.Action, job.Token = "join", strconv.Itoa(port)
	_, err = ctrl.exchange(ctx, c.extraWorker, job)
	return err
}

func (w *ctExtraInstance) joinEasyTier(ctx context.Context, link ConnTestLink, job ctExtraJob) error {
	port, err := strconv.Atoi(job.Token)
	if err != nil {
		return errors.New("invalid EasyTier test port")
	}
	spec, err := ctEasyTierSpec(link, job.Name, job.Mode, link.Host, port, sideKharej)
	if err != nil {
		return err
	}
	w.name, w.mode, w.helper = job.Name, job.Mode, &ctHelperProcess{}
	if err := w.helper.startEasyTier(w.dir, spec); err != nil {
		return err
	}
	return w.listenRoutedTestEcho(ctx, hostOf(spec.LocalIP), link.L3)
}

func (w *ctExtraInstance) listenRoutedTestEcho(ctx context.Context, ip string, port int) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	for ctx.Err() == nil {
		if listener, err := net.Listen("tcp4", addr); err == nil {
			w.echo = &ctEchoes{}
			w.echo.add(listener)
			go ctServeTCPEcho(listener, w.echo)
			return nil
		}
		if exited, detail := w.helper.startupFailure(); exited {
			return errors.New(detail)
		}
		ctSleep(ctx, 100*time.Millisecond)
	}
	return fmt.Errorf("%s did not bring up its test interface", w.helper.label)
}
