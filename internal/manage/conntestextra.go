package manage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/omni"
)

const ctGostDirect = "gost-direct"
const ctOmniPrefix = "omni-"

var ctExtraTestName = regexp.MustCompile("^bpct[a-f0-9]{12}$")
var ctOpaqueToken = regexp.MustCompile("[A-Za-z0-9_+/=-]{40,}")
var ctANSI = regexp.MustCompile("\\x1b\\[[0-9;]*m")

func ctExtraCases() []*connTestCase {
	cases := make([]*connTestCase, 0, len(omni.Options())+1)
	for _, mode := range omni.Options() {
		cases = append(cases, &connTestCase{kind: mode["direction"], tr: ctOmniPrefix + mode["value"], extra: true})
	}
	return append(cases, &connTestCase{kind: "direct", tr: ctGostDirect, extra: true})
}

func ctExtraName() string {
	var id [6]byte
	_, _ = rand.Read(id[:])
	return "bpct" + hex.EncodeToString(id[:])
}

func ctExtraError(err error) string {
	text := ctANSI.ReplaceAllString(err.Error(), "")
	text = strings.Join(strings.Fields(ctOpaqueToken.ReplaceAllString(text, "[private]")), " ")
	runes := []rune(text)
	if len(runes) > 100 {
		text = string(runes[:100]) + "..."
	}
	for len(text) > 125 {
		text = string([]rune(text)[:len([]rune(text))-1])
	}
	return text
}

func ctOmniRun(ctx context.Context, input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
	defer cancel()
	var out, stderr bytes.Buffer
	err := omni.RunContext(ctx, args, strings.NewReader(input), &out, &stderr)
	if err != nil {
		diagnostic := strings.TrimSpace(stderr.String())
		if diagnostic == "" {
			diagnostic = strings.TrimSpace(out.String())
		}
		if diagnostic != "" {
			return "", errors.New(diagnostic)
		}
		return "", err
	}
	return out.String(), nil
}

func ctOmniToken(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		str := strings.TrimSpace(line)
		if len(str) < 32 {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(str)
		if err == nil && bytes.Contains(raw, []byte("|")) {
			return str, nil
		}
	}
	return "", errors.New("the OmniTunnel helper did not produce a setup token")
}

func ctOmniTokenFields(token string) ([]string, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(raw), "|")
	if len(fields) < 9 || net.ParseIP(fields[6]).To4() == nil {
		return nil, errors.New("invalid OmniTunnel setup token")
	}
	return fields, nil
}

func ctExtraRemove(mode, name string) error {
	if !ctExtraTestName.MatchString(name) {
		return errors.New("invalid temporary tunnel name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 32*time.Second)
	defer cancel()
	if mode == ctGostDirect {
		_, err := gostdirect.RunTest(ctx, gostdirect.Form{Action: "remove", Name: name})
		return err
	}
	_, err := ctOmniRun(ctx, "", "remove", name)
	return err
}

// When an auxiliary carrier uses a span of ports, reserve and release the
// whole span on the listening host. Probe ports are outside the default local
// ephemeral range. The actual bind still has the last word.
func ctExtraPort(mode string) (int, error) {
	span := 1
	if mode == "stripe" {
		span = 400
	}
	if mode == "ackless" {
		span = 100
	}
	for attempt := 0; attempt < 30; attempt++ {
		p := ctPickPort(map[int]bool{}, true)
		if span == 1 && p != 0 {
			return p, nil
		}
		if p == 0 || p+span > ephemeralDefaultLow {
			continue
		}
		listeners := make([]net.Listener, 0, span)
		for i := 0; i < span; i++ {
			l, err := net.Listen("tcp", fmt.Sprintf(":%d", p+i))
			if err != nil {
				break
			}
			listeners = append(listeners, l)
		}
		for _, l := range listeners {
			l.Close()
		}
		if len(listeners) == span {
			return p, nil
		}
	}
	return 0, errors.New("no free tunnel port or port range on Kharej")
}

type ctExtraWorker struct {
	mode, name string
	echo       *ctEchoes
}

func (w *ctExtraWorker) close() error {
	if w.echo != nil {
		w.echo.close()
		w.echo = nil
	}
	if w.name == "" {
		return nil
	}
	if err := ctExtraRemove(w.mode, w.name); err != nil {
		return err
	}
	w.name, w.mode = "", ""
	return nil
}

func (w *ctExtraWorker) perform(ctx context.Context, link ConnTestLink, job ctExtraJob) (int, error) {
	if os.Geteuid() != 0 {
		return 0, errors.New("the temporary tunnel requires root on Kharej")
	}
	if !ctExtraTestName.MatchString(job.Name) {
		return 0, errors.New("invalid test instance")
	}
	switch job.Action {
	case "port":
		if job.Mode != ctGostDirect {
			if err := ctCheckOmniTestHelper(ctx); err != nil {
				return 0, err
			}
		}
		return ctExtraPort(job.Mode)
	case "remove":
		if w.name == job.Name {
			return 0, w.close()
		}
		return 0, nil
	case "mux":
		if w.name != job.Name || w.mode != "reverse-mux" {
			return 0, errors.New("the matching Reverse Mux test is not started")
		}
		data, err := base64.StdEncoding.DecodeString(job.Token)
		if err != nil || !bytes.HasPrefix(data, []byte("MUX2|"+job.Name+"|")) {
			return 0, errors.New("invalid Reverse Mux setup token")
		}
		_, err = ctOmniRun(ctx, job.Token, "mux-join")
		return 0, err
	case "join":
		if err := w.close(); err != nil {
			return 0, err
		}
		return 0, w.join(ctx, link, job)
	}
	return 0, errors.New("invalid test operation")
}

func (w *ctExtraWorker) join(ctx context.Context, link ConnTestLink, job ctExtraJob) error {
	if job.Mode == ctGostDirect {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(job.Token, "bpgost1."))
		var token struct {
			Name   string
			Target int
		}
		if err != nil || json.Unmarshal(raw, &token) != nil || token.Name != job.Name || token.Target != link.TCP {
			return errors.New("invalid GOST test token")
		}
		w.name, w.mode = job.Name, job.Mode
		ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
		defer cancel()
		_, err = gostdirect.RunTest(ctx, gostdirect.Form{Action: "join", Token: job.Token})
		return err
	}
	fields, err := ctOmniTokenFields(job.Token)
	if err != nil || fields[0] != job.Name || fields[1] != job.Mode {
		return errors.New("invalid OmniTunnel test token")
	}
	w.name, w.mode = job.Name, job.Mode
	if _, err := ctOmniRun(ctx, job.Token, "join"); err != nil {
		return err
	}
	if job.Mode == "reverse-mux" || job.Mode == "hysteria" {
		return nil
	}
	// The real TUN/address is required. Bind the existing speed/echo protocol
	// on it; a host-level probe cannot count as a successful tunnel test.
	var l net.Listener
	for attempt := 0; attempt < 12 && ctx.Err() == nil; attempt++ {
		l, err = net.Listen("tcp", net.JoinHostPort(fields[6], strconv.Itoa(link.L3)))
		if err == nil {
			break
		}
		ctSleep(ctx, 250*time.Millisecond)
	}
	if err != nil || l == nil {
		return fmt.Errorf("cannot bind the tunnel echo on Kharej: %w", err)
	}
	w.echo = &ctEchoes{}
	w.echo.add(l)
	go ctServeTCPEcho(l, w.echo)
	return nil
}

func ctCheckOmniTestHelper(ctx context.Context) error {
	out, err := ctOmniRun(ctx, "", "test-ready")
	if err != nil || strings.TrimSpace(out) != "backpack-omni-test-v1" {
		return errors.New("install or refresh the fork's OmniTunnel helper (BP_HELPERS=omni)")
	}
	return nil
}

func ctStartExtraWorker(ctx context.Context, link ConnTestLink, out io.Writer) func() {
	key, err := ctExtraCipher(link.pairKey)
	if err != nil {
		return nil
	}
	hello, err := ctExtraSeal(key, "ready", true)
	if err != nil {
		return nil
	}
	register, cancelRegister := context.WithTimeout(ctx, 12*time.Second)
	reply, err := ctAskContext(register, link.Host, link.Coord, "extra-ready "+link.Tok+" "+hello)
	cancelRegister()
	if err != nil || reply != "ok" {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctExtraWork(ctx, link, out)
	}()
	return func() { cancel(); <-done }
}

func ctExtraWork(ctx context.Context, link ConnTestLink, out io.Writer) {
	key, err := ctExtraCipher(link.pairKey)
	if err != nil {
		return
	}
	w := &ctExtraWorker{}
	defer func() {
		if err := w.close(); err != nil {
			fmt.Fprintln(out, "Temporary transport cleanup:", ctExtraError(err))
		}
	}()
	var sequence uint64
	var reply string
	for ctx.Err() == nil {
		job, err := ctExtraReadJob(ctx, link, key)
		if err != nil || job.ID == 0 || job.ID < sequence {
			ctSleep(ctx, 300*time.Millisecond)
			continue
		}
		if job.ID != sequence {
			result := ctExtraReply{ID: job.ID}
			result.Port, err = w.perform(ctx, link, job)
			if err != nil {
				result.Error = ctExtraError(err)
			}
			reply, err = ctExtraSeal(key, "reply", result)
			if err != nil {
				return
			}
			sequence = job.ID
		}
		_, _ = ctAskContext(ctx, link.Host, link.Coord, "extra-reply "+link.Tok+" "+reply)
		ctSleep(ctx, 300*time.Millisecond)
	}
}

func (s *ConnTestIran) setupExtra(ctx context.Context, c *connTestCase, mode string) error {
	ctrl := s.coord.extra
	ctrl.mu.Lock()
	ready := ctrl.ready
	ctrl.mu.Unlock()
	if !ready {
		return errors.New("Kharej did not enable the transport-test control channel; update both servers")
	}
	near, far := ctIPv4Context(ctx, s.link.Host), ctIPv4Context(ctx, s.Kharej())
	if near == "" || far == "" || near == far {
		return errors.New("this carrier requires two different IPv4 endpoints")
	}
	if mode != ctGostDirect {
		if err := ctCheckOmniTestHelper(ctx); err != nil {
			return err
		}
	}
	job := ctExtraJob{Action: "port", Name: c.name, Mode: mode}
	r, err := ctrl.exchange(ctx, job)
	if err != nil {
		return err
	}
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("invalid test port selected on Kharej")
	}
	port := strconv.Itoa(r.Port)
	if mode == ctGostDirect {
		c.entry = ctPickPort(map[int]bool{}, false)
		if c.entry == 0 {
			return errors.New("no free GOST ingress port on Iran")
		}
		ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
		defer cancel()
		c.localStarted = true
		result, err := gostdirect.RunTest(ctx, gostdirect.Form{Action: "setup", Name: c.name, Peer: far, Port: port, Target: strconv.Itoa(s.link.TCP), Ingress: strconv.Itoa(c.entry)})
		if err != nil {
			return err
		}
		job.Action, job.Token = "join", result.Token
		c.peerStarted = true
		_, err = ctrl.exchange(ctx, job)
		return err
	}
	c.localStarted = true
	output, err := ctOmniRun(ctx, "", "setup", mode, c.name, near, far, port)
	if err != nil {
		return err
	}
	token, err := ctOmniToken(output)
	if err != nil {
		return err
	}
	fields, err := ctOmniTokenFields(token)
	if err != nil {
		return err
	}
	job.Action, job.Token = "join", token
	c.peerStarted = true
	if _, err = ctrl.exchange(ctx, job); err != nil {
		return err
	}
	c.peerIP = fields[6]
	if mode != "hysteria" && mode != "reverse-mux" {
		return nil
	}
	c.entry = ctPickPort(map[int]bool{}, false)
	if c.entry == 0 {
		return errors.New("no free forwarding port on Iran")
	}
	entry, target := strconv.Itoa(c.entry), strconv.Itoa(s.link.TCP)
	if mode == "hysteria" {
		_, err = ctOmniRun(ctx, "", "forward", c.name, "tcp", entry+":"+target)
		return err
	}
	output, err = ctOmniRun(ctx, "", "mux-forward", c.name, entry, "127.0.0.1", target)
	if err != nil {
		return err
	}
	token, err = ctOmniToken(output)
	if err != nil {
		return err
	}
	job.Action, job.Token = "mux", token
	_, err = ctrl.exchange(ctx, job)
	return err
}

func (s *ConnTestIran) probeExtra(ctx context.Context, c *connTestCase, report func(ConnTestResult)) {
	mode := strings.TrimPrefix(c.tr, ctOmniPrefix)
	c.name = ctExtraName()
	row := ConnTestResult{Kind: c.kind, Transport: c.tr, Status: ctTesting, Total: connTestSoak, Detail: "preparing temporary tunnel"}
	report(row)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		var peerErr, localErr error
		if c.peerStarted {
			_, peerErr = s.coord.extra.exchange(ctx, ctExtraJob{Action: "remove", Name: c.name, Mode: mode})
		}
		if c.localStarted {
			localErr = ctExtraRemove(mode, c.name)
		}
		if localErr != nil || peerErr != nil {
			row.Detail = strings.Trim(strings.Join([]string{row.Detail, "temporary tunnel cleanup could not be confirmed"}, "; "), "; ")
			report(row)
		}
	}()
	if err := s.setupExtra(ctx, c, mode); err != nil {
		row.Status, row.Detail = ctSkipped, "Could not prepare tunnel: "+ctExtraError(err)
		report(row)
		return
	}
	row = ctProbe(ctx, c, s.link.L3, report)
	if row.Phase != "speed-queued" {
		return
	}
	row.Phase, row.Detail = "speed", "measuring download and upload"
	report(row)
	dial := func() (net.Conn, error) { return ctDial(ctx, c, s.link.L3) }
	speed, err := ctSpeed(ctx, dial)
	if err != nil {
		row.Status, row.Phase, row.Detail = ctUnstable, "", "speed test failed ("+ctShortErr(err)+")"
	} else {
		ctPutSpeed(&row, ctSpeedAgain(ctx, dial, speed))
	}
	report(row)
}
