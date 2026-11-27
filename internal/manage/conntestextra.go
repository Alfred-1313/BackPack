package manage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/internal/easytier"
	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/matrix"
	"github.com/backpack/backpack/internal/omni"
)

const ctGostDirect = gostdirect.ModeV3
const ctOmniPrefix = "omni-"

const (
	ctPhaseInitialEcho      = "transport-initial"
	ctPhaseEchoVerified     = "transport-verified"
	ctPhaseEchoRetry        = "transport-retry"
	ctPhaseTransportQueued  = "transport-queued"
	ctPhaseStabilityReady   = "stability-ready"
	ctPhaseTransportSetup   = "transport-setup"
	ctPhaseTransportConnect = "transport-connect"
	ctPhaseTransportEcho    = "transport-echo"
	ctPhaseTransportCleanup = "transport-cleanup"
)

var ctExtraTestName = regexp.MustCompile("^bpct[a-f0-9]{12}$")
var ctOpaqueToken = regexp.MustCompile("[A-Za-z0-9_+/=-]{40,}")
var ctANSI = regexp.MustCompile("\\x1b\\[[0-9;]*m")

const ctStoppedCleanupLimit = 15 * time.Second

func ctExtraCases() []*connTestCase {
	cases := make([]*connTestCase, 0, len(omni.Options())+4)
	cases = append(cases,
		&connTestCase{kind: matrix.Direct, tr: matrix.DirectID, extra: true},
		&connTestCase{kind: matrix.Reverse, tr: matrix.ReverseID, extra: true})
	cases = append(cases, ctEasyTierCases()...)
	for _, mode := range omni.Options() {
		cases = append(cases, &connTestCase{kind: mode["direction"], tr: ctOmniPrefix + mode["value"], extra: true})
	}
	for _, mode := range []string{gostdirect.ModeV3, gostdirect.ModeV2} {
		cases = append(cases, &connTestCase{kind: "direct", tr: mode, extra: true})
	}
	return append(cases, ctBackhaulCases()...)
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

func ctExtraRemove(ctx context.Context, mode, name string) error {
	if !ctExtraTestName.MatchString(name) {
		return errors.New("invalid temporary tunnel name")
	}
	ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
	defer cancel()
	if gostdirect.ModeVersion(mode) != 0 {
		_, err := gostdirect.RunTest(ctx, gostdirect.Form{Action: "remove", Name: name})
		return err
	}
	_, err := ctOmniRun(ctx, "", "remove", name)
	return err
}

// When an auxiliary carrier uses a span of ports, reserve and release the
// whole span on the listening host. Probe ports are outside the default local
// ephemeral range. The actual bind still has the last word.
type ctExtraPorts struct {
	mu       sync.Mutex
	reserved map[int]bool
}

func (a *ctExtraPorts) take(mode string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reserved == nil {
		a.reserved = make(map[int]bool)
	}
	span := 1
	if mode == "stripe" {
		span = 400
	}
	if mode == "ackless" {
		span = 100
	}
	for attempt := 0; attempt < 30; attempt++ {
		p := ctPickPort(map[int]bool{}, true)
		if p == 0 || p+span > ephemeralDefaultLow {
			continue
		}
		listeners := make([]net.Listener, 0, span)
		for i := 0; i < span; i++ {
			if a.reserved[p+i] {
				break
			}
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
			for port := p; port < p+span; port++ {
				a.reserved[port] = true
			}
			if mode == "icmp" || mode == "reverse-mux" {
				a.reserved[p-1], a.reserved[p+1] = true, true
			}
			return p, nil
		}
	}
	return 0, errors.New("no free tunnel port or port range on Kharej")
}

type ctExtraWorker struct {
	dir       string
	parallel  bool
	ports     *ctExtraPorts
	instances map[string]*ctExtraInstance
}

type ctExtraInstance struct {
	dir        string
	mode, name string
	echo       *ctEchoes
	helper     *ctHelperProcess
}

func (w *ctExtraWorker) close(ctx context.Context, name string) error {
	instance, ok := w.instances[name]
	if !ok {
		return nil
	}
	if err := instance.close(ctx); err != nil {
		return err
	}
	delete(w.instances, name)
	return nil
}

func (w *ctExtraInstance) close(ctx context.Context) error {
	if w.echo != nil {
		w.echo.close()
		w.echo = nil
	}
	if w.helper != nil {
		w.helper.close()
		w.helper, w.name, w.mode = nil, "", ""
	}
	if w.name == "" {
		return nil
	}
	if err := ctExtraRemove(ctx, w.mode, w.name); err != nil {
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
		if _, _, ok := ctEasyTierMode(job.Mode); ok {
			if _, err := easytier.Binary(); err != nil {
				return 0, err
			}
		} else if matrix.IsDirectionalMode(job.Mode) {
			if _, err := matrix.Binary(); err != nil {
				return 0, err
			}
		} else if gostdirect.ModeVersion(job.Mode) == 0 {
			if err := ctCheckOmniTestHelper(ctx); err != nil {
				return 0, err
			}
		}
		return w.ports.take(job.Mode)
	case "remove":
		return 0, w.close(ctx, job.Name)
	case "mux":
		instance := w.instances[job.Name]
		if instance == nil || instance.mode != "reverse-mux" {
			return 0, errors.New("the matching Reverse Mux test is not started")
		}
		data, err := base64.StdEncoding.DecodeString(job.Token)
		if err != nil || !bytes.HasPrefix(data, []byte("MUX2|"+job.Name+"|")) {
			return 0, errors.New("invalid Reverse Mux setup token")
		}
		_, err = ctOmniRun(ctx, job.Token, "mux-join")
		return 0, err
	case "join":
		if !w.parallel {
			for name := range w.instances {
				if err := w.close(ctx, name); err != nil {
					return 0, err
				}
			}
		}
		if w.instances[job.Name] != nil || len(w.instances) >= len(ctExtraCases())+len(ctDaggerCases()) {
			return 0, errors.New("no slot for this temporary test tunnel")
		}
		instance := &ctExtraInstance{dir: w.dir}
		err := instance.join(ctx, link, job)
		if instance.name != "" {
			w.instances[job.Name] = instance
		}
		return 0, err
	}
	return 0, errors.New("invalid test operation")
}

func (w *ctExtraInstance) join(ctx context.Context, link ConnTestLink, job ctExtraJob) error {
	if _, _, ok := ctEasyTierMode(job.Mode); ok {
		return w.joinEasyTier(ctx, link, job)
	}
	if matrix.IsDirectionalMode(job.Mode) {
		return w.joinMatrix(ctx, link, job)
	}
	if strings.HasPrefix(job.Mode, ctBackhaulPrefix) {
		return w.joinBackhaul(ctx, link, job)
	}
	if _, ok := strings.CutPrefix(job.Mode, ctDaggerPrefix); ok {
		return w.joinDagger(ctx, link, job)
	}
	if version := gostdirect.ModeVersion(job.Mode); version != 0 {
		token, err := gostdirect.ReadTokenInfo(job.Token)
		if err != nil || token.Name != job.Name || token.Target != link.TCP || token.Version != version {
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
	if _, err := ctOmniRun(ctx, job.Token, "join", job.Mode); err != nil {
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

func ctStartExtraWorker(ctx context.Context, link ConnTestLink, out io.Writer, dir string) func() {
	key, err := ctExtraCipher(link.pairKey)
	if err != nil {
		return nil
	}
	workers := 1
	var readiness any = true
	if link.ParallelExtra {
		readiness = ctExtraParallel
		if link.ParallelSetup {
			workers = ctExtraSetupWorkers
			readiness = ctExtraParallelSetup
		}
	}
	hello, err := ctExtraSeal(key, "ready", readiness)
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
	var workersDone sync.WaitGroup
	ports := &ctExtraPorts{}
	var cleanupLog sync.Mutex
	reportCleanup := func(err error) {
		cleanupLog.Lock()
		defer cleanupLog.Unlock()
		fmt.Fprintln(out, "Temporary transport cleanup:", ctExtraError(err))
	}
	for worker := range workers {
		workersDone.Go(func() { ctExtraWork(ctx, link, worker, ports, reportCleanup, dir) })
	}
	return func() { cancel(); workersDone.Wait() }
}

func ctExtraWork(ctx context.Context, link ConnTestLink, worker int, ports *ctExtraPorts, reportCleanup func(error), dir string) {
	key, err := ctExtraCipher(link.pairKey)
	if err != nil {
		return
	}
	w := &ctExtraWorker{dir: dir, parallel: link.ParallelExtra, ports: ports, instances: make(map[string]*ctExtraInstance)}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), ctStoppedCleanupLimit)
		defer cancel()
		var removals sync.WaitGroup
		for _, instance := range w.instances {
			removals.Go(func() {
				if err := instance.close(cleanup); err != nil {
					reportCleanup(err)
				}
			})
		}
		removals.Wait()
	}()
	var sequence uint64
	var reply string
	for ctx.Err() == nil {
		job, err := ctExtraReadJob(ctx, link, key, worker)
		if errors.Is(err, errConnTestStopped) {
			return
		}
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
	if _, _, ok := ctEasyTierMode(mode); ok {
		return s.setupEasyTier(ctx, c, ctrl)
	}
	if matrix.IsDirectionalMode(mode) {
		return s.setupMatrix(ctx, c, ctrl)
	}
	if strings.HasPrefix(mode, ctBackhaulPrefix) {
		return s.setupBackhaul(ctx, c, mode)
	}
	if _, ok := strings.CutPrefix(mode, ctDaggerPrefix); ok {
		return s.setupDagger(ctx, c, mode)
	}
	near, far := ctIPv4Context(ctx, s.link.Host), ctIPv4Context(ctx, s.Kharej())
	if near == "" || far == "" || near == far {
		return errors.New("this carrier requires two different IPv4 endpoints")
	}
	gostVersion := gostdirect.ModeVersion(mode)
	if gostVersion == 0 {
		if err := ctCheckOmniTestHelper(ctx); err != nil {
			return err
		}
	}
	job := ctExtraJob{Action: "port", Name: c.name, Mode: mode}
	r, err := ctrl.exchange(ctx, c.extraWorker, job)
	if err != nil {
		return err
	}
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("invalid test port selected on Kharej")
	}
	port := strconv.Itoa(r.Port)
	if gostVersion != 0 {
		c.entry = ctPickPort(map[int]bool{}, false)
		if c.entry == 0 {
			return errors.New("no free GOST ingress port on Iran")
		}
		ctx, cancel := context.WithTimeout(ctx, 32*time.Second)
		defer cancel()
		c.localStarted = true
		result, err := gostdirect.RunTest(ctx, gostdirect.Form{Action: "setup", Name: c.name, Peer: far,
			Port: port, Target: strconv.Itoa(s.link.TCP), Ingress: strconv.Itoa(c.entry), Version: gostVersion})
		if err != nil {
			return err
		}
		job.Action, job.Token = "join", result.Token
		c.peerStarted = true
		_, err = ctrl.exchange(ctx, c.extraWorker, job)
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
	if err != nil || fields[0] != c.name || fields[1] != mode || fields[2] != far || fields[3] != near {
		return errors.New("the helper returned settings for a different test tunnel")
	}
	job.Action, job.Token = "join", token
	c.peerStarted = true
	if _, err = ctrl.exchange(ctx, c.extraWorker, job); err != nil {
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
	_, err = ctrl.exchange(ctx, c.extraWorker, job)
	return err
}

func (s *ConnTestIran) removeExtra(ctx context.Context, c *connTestCase) error {
	mode := strings.TrimPrefix(c.tr, ctOmniPrefix)
	var peerErr error
	if c.peerStarted {
		ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		_, peerErr = s.coord.extra.exchange(ctx, c.extraWorker, ctExtraJob{Action: "remove", Name: c.name, Mode: mode})
		c.peerStarted = peerErr != nil
	}
	return errors.Join(peerErr, s.removeExtraLocal(ctx, c))
}

func (s *ConnTestIran) removeExtraLocal(ctx context.Context, c *connTestCase) error {
	var err error
	if c.localStarted {
		if c.helper != nil {
			c.helper.close()
			c.helper = nil
		} else {
			err = ctExtraRemove(ctx, strings.TrimPrefix(c.tr, ctOmniPrefix), c.name)
		}
		c.localStarted = err != nil
	}
	return err
}

func (s *ConnTestIran) prepareExtra(ctx context.Context, c *connTestCase, report func(ConnTestResult)) (ConnTestResult, bool) {
	row := ConnTestResult{Kind: c.kind, Transport: c.tr, Status: ctTesting, Total: connTestSoak}
	if ctx.Err() != nil {
		row.Status, row.Detail = ctStopped, "test stopped before tunnel setup"
		report(row)
		return row, false
	}
	c.name = ctExtraName()
	row.Phase, row.Detail = ctPhaseTransportSetup, "preparing temporary tunnel"
	report(row)
	if err := s.setupExtra(ctx, c, strings.TrimPrefix(c.tr, ctOmniPrefix)); err != nil {
		row.Status, row.Detail, row.Phase = ctSkipped, "Could not prepare tunnel: "+ctExtraError(err), ""
		if ctx.Err() != nil {
			row.Status, row.Detail = ctStopped, "stopped during tunnel setup"
		}
		report(row)
		return row, false
	}
	return row, true
}

func (s *ConnTestIran) probeStageExtras(ctx context.Context, indices []int, workers int, retain bool, report func(int, ConnTestResult)) {
	queue := make(chan int, len(indices))
	for _, i := range indices {
		queue <- i
	}
	close(queue)
	var setups, probes sync.WaitGroup
	for worker := range min(workers, len(indices)) {
		setups.Go(func() {
			for i := range queue {
				c := s.cases[i]
				c.extraWorker = worker
				update := func(row ConnTestResult) { report(i, row) }
				row, ok := s.prepareExtra(ctx, c, update)
				if retain {
					if ok {
						probes.Go(func() { s.extraStability(ctx, c, update) })
					}
					continue
				}
				if ok {
					row = s.extraStability(ctx, c, update)
				}
				s.cleanupExtra(ctx, c, row, update)
			}
		})
	}
	setups.Wait()
	probes.Wait()
}

func (s *ConnTestIran) extraStability(ctx context.Context, c *connTestCase, report func(ConnTestResult)) ConnTestResult {
	return ctProbe(ctx, c, s.link.L3, func(r ConnTestResult) {
		if r.Status == ctTesting && r.Phase == "" {
			r.Phase, r.Detail = ctPhaseTransportEcho, "checking stability through the tunnel"
		}
		report(r)
	})
}

const ctCleanupUnconfirmed = "temporary tunnel cleanup could not be confirmed"

func (s *ConnTestIran) cleanupExtra(ctx context.Context, c *connTestCase, row ConnTestResult, report func(ConnTestResult)) {
	if ctx.Err() != nil || (!c.localStarted && !c.peerStarted) {
		return
	}
	cleaning := row
	cleaning.Phase, cleaning.Detail = ctPhaseTransportCleanup, "removing temporary tunnel on both servers"
	report(cleaning)
	row.Detail = strings.TrimSuffix(strings.TrimSuffix(row.Detail, ctCleanupUnconfirmed), "; ")
	if err := s.removeExtra(ctx, c); err != nil && ctx.Err() == nil {
		row.Detail = strings.Trim(strings.Join([]string{row.Detail, ctCleanupUnconfirmed}, "; "), "; ")
	}
	report(row)
}

// Each lane keeps its removal requests in order while independent lanes clean up concurrently.
func (s *ConnTestIran) cleanupExtraCases(ctx context.Context, workers int, rows []ConnTestResult, report func(int, ConnTestResult), includeSpeedQueued bool) {
	var cleanup sync.WaitGroup
	for worker := range workers {
		cleanup.Go(func() {
			for i, c := range s.cases {
				if c.extra && c.extraWorker == worker && (includeSpeedQueued || rows[i].Phase != "speed-queued") {
					s.cleanupExtra(ctx, c, rows[i], func(row ConnTestResult) { report(i, row) })
				}
			}
		})
	}
	cleanup.Wait()
}

// Finish local removals that were interrupted or failed during the run.
// Local cleanup has its own deadline and does not depend on the peer.
func (s *ConnTestIran) cleanupLocalExtras(rows []ConnTestResult, report func(int, ConnTestResult)) {
	ctx, cancel := context.WithTimeout(context.Background(), ctStoppedCleanupLimit)
	defer cancel()
	var removals sync.WaitGroup
	for i, c := range s.cases {
		if !c.extra || !c.localStarted {
			continue
		}
		removals.Go(func() {
			row := rows[i]
			detail := strings.TrimSuffix(strings.TrimSuffix(row.Detail, ctCleanupUnconfirmed), "; ")
			if err := s.removeExtraLocal(ctx, c); err != nil || (c.peerStarted && detail != row.Detail) {
				detail = strings.Trim(strings.Join([]string{detail, ctCleanupUnconfirmed}, "; "), "; ")
			}
			if detail != row.Detail {
				row.Detail = detail
				report(i, row)
			}
		})
	}
	removals.Wait()
}

// Rebuild a helper that was cleaned up after stability. Run both speed
// passes before returning its slot.
func (s *ConnTestIran) speedRebuiltExtra(ctx context.Context, c *connTestCase) (ctSpeedResult, error) {
	if c.localStarted || c.peerStarted {
		return ctSpeedResult{}, errors.New("the previous temporary tunnel was not fully removed")
	}
	c.name = ctExtraName()
	if err := s.setupExtra(ctx, c, strings.TrimPrefix(c.tr, ctOmniPrefix)); err != nil {
		return ctSpeedResult{}, err
	}
	dial := func() (net.Conn, error) { return ctDial(ctx, c, s.link.L3) }
	speed, err := ctSpeed(ctx, dial)
	if err == nil {
		speed = ctSpeedAgain(ctx, dial, speed)
	}
	return speed, err
}
