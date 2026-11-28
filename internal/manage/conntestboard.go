package manage

import (
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/matrix"
	"github.com/backpack/backpack/internal/omni"
	"golang.org/x/sys/unix"
)

// The Connection Test's table, drawn while the test runs.
//
// Each tunnel is one row, and a row changes as its echoes come back: STATUS
// reads Testing, ECHOES counts the verified replies, and the progress bar
// fills with the attempts made. When the test ends the table is
// set out once more as the verdict: the rows grouped OK, UNSTABLE, DOWN, each
// bar filled by the echoes that came back, and under it the tunnels that
// carried everything, with their round trip and speed.

// ctBarSegments is how many segments a bar has.
const ctBarSegments = 8

// ctTransportNames are the names the menus use.
var ctTransportNames = map[string]string{
	matrix.ID: matrix.Label, matrix.DirectID: matrix.Label, matrix.ReverseID: matrix.Label,
	"tcp": "TCP", "tcpmux": "TCP MUX", "stealth": "Stealth", "pck": "PCK",
	"ws": "WS", "wss": "WSS", "wsmux": "WS MUX", "wssmux": "WSS MUX",
	"kcp": "KCP FEC", "quic": "QUIC", "udp": "UDP", "udp-padder": "UDP Padder", "xdi": "xDi", ctXdiFEC: "xDi + FEC", "sni": "SNI", "gre": "GRE",
	"gretap": "GRETAP", "l2tpv3": "L2TPv3 (IP)", "easytier": "EasyTier / TCP",
	"naive": "Naive / HTTP2", "anytls": "AnyTLS", "xhttp": "XHTTP / TLS", "reality": "REALITY / Vision", "gost": "GOST 3",
	ctSpoofToKharej: "Iran→Kharej", ctSpoofToIran: "Kharej→Iran",
}

func ctName(tr string) string {
	if mode, _, ok := ctPenguMode(tr); ok {
		return mode.Label
	}
	if _, scheme, ok := ctEasyTierMode(tr); ok {
		return "EasyTier / " + strings.ToUpper(scheme)
	}
	if version := gostdirect.ModeVersion(tr); version != 0 {
		return fmt.Sprintf("GOST %d", version)
	}
	if mode, ok := strings.CutPrefix(tr, ctOmniPrefix); ok {
		return omni.Label(mode)
	}
	if ctBackhaulTransport(tr) {
		return ctBackhaulLabel(tr)
	}
	if label := ctDaggerLabel(tr); label != "" {
		return label
	}
	if n, ok := ctTransportNames[tr]; ok {
		return n
	}
	return strings.ToUpper(tr)
}

func ctKind(kind string) string {
	if kind == "" {
		return ""
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// ctBar is a bar filled to share (0–1), with the percentage beside it.
func ctBar(share float64) string {
	share = math.Max(0, math.Min(1, share))
	filled := int(math.Round(share * ctBarSegments))
	return fmt.Sprintf("%s%s %3d%%", strings.Repeat("▰", filled),
		strings.Repeat("▱", ctBarSegments-filled), int(math.Round(share*100)))
}

func ctEmoji(status string) string {
	switch status {
	case ctOK:
		return "🟢"
	case ctDown:
		return "🔴"
	case ctSkipped, ctStopped:
		return "⚪"
	default: // testing, unstable
		return "🟡"
	}
}

const ctRowFormat = "%-7s  %-16s  %-13s  %6s  %s"

func ctTableHeader(meter string) string {
	return fmt.Sprintf(ctRowFormat, "KIND", "TRANSPORT", "STATUS", "ECHOES", meter)
}

// ctRow shows verified replies and, while running, progress through the attempts.
func ctRow(r ConnTestResult) string {
	echoes, progress := "—", ""
	if ctHasEchoCount(r) {
		total := r.Total
		if total <= 0 {
			total = connTestSoak
		}
		done := r.OK
		if r.Status == ctTesting {
			done = r.Tried
		}
		echoes = fmt.Sprintf("%d/%d", r.OK, total)
		progress = ctBar(float64(done) / float64(total))
	}
	return strings.TrimRight(fmt.Sprintf(ctRowFormat, ctKind(r.Kind), ctName(r.Transport),
		ctStatusLabel(r), echoes, progress), " ")
}

func ctHasEchoCount(r ConnTestResult) bool {
	if r.Tried > 0 || r.OK > 0 {
		return true
	}
	switch r.Phase {
	case ctPhaseStageQueued, ctPhaseTransportQueued, ctPhaseStabilityReady, ctPhaseTransportSetup, ctPhaseTransportConnect,
		ctPhaseInitialEcho, ctPhaseEchoVerified, ctPhaseEchoRetry, ctPhaseTransportCleanup:
		return false
	}
	return r.Status != ctSkipped && r.Status != ctStopped
}

func ctStatusLabel(r ConnTestResult) string {
	switch r.Phase {
	case ctPhaseStageQueued:
		return "Waiting"
	case "speed":
		return "Speed test"
	case ctPhaseSecond:
		return "Speed retest"
	case "speed-queued":
		return "Speed queued"
	case ctPhaseAgain:
		return "Retest queued"
	case ctPhaseTransportQueued:
		return "Queued"
	case ctPhaseStabilityReady:
		return "Ready"
	case ctPhaseInitialEcho:
		return "First echo"
	case ctPhaseEchoVerified:
		return "First echo OK"
	case ctPhaseEchoRetry:
		return "Retry pending"
	case ctPhaseTransportSetup:
		return "Setting up"
	case ctPhaseTransportConnect:
		return "Connecting"
	case ctPhaseTransportEcho:
		return "Testing"
	case ctPhaseTransportCleanup:
		return "Cleaning up"
	}
	if r.Status == ctOK {
		return "OK"
	}
	return ctKind(r.Status)
}

func ctRule() string { return strings.Repeat("─", 63) }

// ConnTestTable is the verdict as text, grouped by transport kind.
func ConnTestTable(results []ConnTestResult) string {
	var b strings.Builder
	for _, kind := range ctTestKinds {
		rows := ctStageRows(results, kind)
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintln(&b, strings.ToUpper(kind))
		fmt.Fprintln(&b, ctTableHeader("STABILITY"))
		for _, group := range ctGroups(rows) {
			for _, r := range group {
				fmt.Fprintln(&b, ctRow(r))
			}
		}
		fmt.Fprintln(&b)
	}
	b.WriteString(ctRule() + "\n\nWorks Steadily:\n\n")
	steady := 0
	for _, group := range ctGroups(results) {
		for _, r := range group {
			if r.Status != ctOK {
				continue
			}
			steady++
			rtt, speed := "-", "-"
			// The spoofing probes carry no reply, so they have no round trip.
			if r.RTTms > 0 {
				rtt = fmt.Sprintf("%dms", r.RTTms)
			} else if r.OK > 0 && r.Kind != "spoof" {
				rtt = "<1ms"
			}
			// The most it carried leads; the sustained rate follows.
			if r.Mbps > 0 {
				speed = fmt.Sprintf("%.1f Mbps", r.Mbps)
				if r.UploadMbps > 0 {
					speed = fmt.Sprintf("↓ %.1f / ↑ %.1f Mbps", r.Mbps, r.UploadMbps)
				}
				if r.PeakMbps > 0 {
					speed = fmt.Sprintf("max ↓ %.1f / ↑ %.1f Mbps · avg ↓ %.1f / ↑ %.1f",
						max(r.PeakMbps, r.Mbps), max(r.UploadPeakMbps, r.UploadMbps), r.Mbps, r.UploadMbps)
				}
			}
			fmt.Fprintf(&b, "%s %-26s %-7s %s\n", ctEmoji(ctOK), ctKind(r.Kind)+" "+ctName(r.Transport), rtt, speed)
		}
	}
	if steady == 0 {
		b.WriteString("Nothing carried traffic steadily between these two servers. See the\n" +
			"statuses and notes for setup errors, interruption or path failures.\n")
	}
	// Why, where the table alone cannot say: a spoofing check that failed, and
	// anything that was not run.
	var notes []string
	for _, r := range results {
		if r.Detail != "" && r.Status != ctOK {
			notes = append(notes, fmt.Sprintf("%s %s %s: %s", ctEmoji(r.Status), ctKind(r.Kind), ctName(r.Transport), r.Detail))
		}
	}
	if len(notes) > 0 {
		b.WriteString("\nNotes:\n\n" + strings.Join(notes, "\n") + "\n")
	}
	ctHTTPSNotes(&b, results)
	return b.String()
}

func ctStageRows(rows []ConnTestResult, kind string) []ConnTestResult {
	var stage []ConnTestResult
	for _, r := range rows {
		if r.Kind == kind && r.Transport != "" {
			stage = append(stage, r)
		}
	}
	return stage
}

func ctSpeedPhase(phase string) bool {
	switch phase {
	case "speed-queued", "speed", ctPhaseAgain, ctPhaseSecond:
		return true
	}
	return false
}

func ctHTTPSNotes(out io.Writer, results []ConnTestResult) {
	shown := false
	for _, r := range results {
		if r.Kind != "reverse" || !managedTransport(r.Transport) || r.Status == ctSkipped || r.TestPort < 1 || r.TestPort > 65535 {
			continue
		}
		if !shown {
			fmt.Fprint(out, "\nHTTPS path checks (separate connections):\n\n")
			shown = true
		}
		label := ctHTTPSProbeLabel(r.HTTPSProbe)
		if r.Transport == "gost" {
			label = strings.ReplaceAll(label, "/HTTP2", "")
		}
		fmt.Fprintf(out, "  %-16s :%-5d %s\n", ctName(r.Transport), r.TestPort, label)
	}
	if shown {
		fmt.Fprintln(out, "  Tunnel status above requires real authenticated, sustained traffic.")
		fmt.Fprintln(out, "  Temporary ports/private test certificates differ from a real HTTPS site.")
		fmt.Fprintln(out, "  Changing SNI alone cannot fix a route that drops established flows.")
	}
}

// ctGroups orders the verdict as the table shows it: what worked, fastest
// first; what dropped, most echoes first; then what never came up, reverse and
// direct apart; then what was not run.
func ctGroups(results []ConnTestResult) [][]ConnTestResult {
	var ok, unstable, downReverse, downDirect, downSpoof, skipped []ConnTestResult
	for _, r := range results {
		switch r.Status {
		case ctOK:
			ok = append(ok, r)
		case ctDown:
			switch r.Kind {
			case "direct":
				downDirect = append(downDirect, r)
			case "spoof":
				downSpoof = append(downSpoof, r)
			default:
				downReverse = append(downReverse, r)
			}
		case ctSkipped, ctStopped:
			skipped = append(skipped, r)
		default:
			unstable = append(unstable, r)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].Mbps > ok[j].Mbps })
	sort.SliceStable(unstable, func(i, j int) bool { return unstable[i].OK > unstable[j].OK })
	var out [][]ConnTestResult
	for _, g := range [][]ConnTestResult{ok, unstable, downReverse, downDirect, downSpoof, skipped} {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// ctBoard draws the live table and keeps it current.
type ctBoard struct {
	out   io.Writer
	title string
	tty   bool

	mu         sync.Mutex
	rows       []ConnTestResult
	shown      []string
	inPlace    bool
	snapshotAt time.Time
	stop       chan struct{}
	done       chan struct{}
}

// newCTBoard starts a board under title. On a terminal it redraws changed
// rows in place a few times a second; anywhere else it prints only the
// verdict.
func newCTBoard(out io.Writer, title string) *ctBoard {
	b := &ctBoard{out: out, title: title, tty: ctIsTerminal(out),
		stop: make(chan struct{}), done: make(chan struct{})}
	go b.loop()
	return b
}

func ctIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// setAll replaces every row, for the kharej, which is sent them together.
func (b *ctBoard) setAll(rows []ConnTestResult) {
	b.mu.Lock()
	b.rows = append([]ConnTestResult(nil), rows...)
	b.mu.Unlock()
}

func (b *ctBoard) loop() {
	defer close(b.done)
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
			if b.tty {
				b.draw()
			}
		}
	}
}

func (b *ctBoard) lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.rows) == 0 {
		return nil
	}
	out := []string{"CONNECTION TEST — " + b.title, ctRule(), ctTableHeader("PROGRESS")}
	for _, kind := range ctTestKinds {
		rows := ctStageRows(b.rows, kind)
		if len(rows) == 0 {
			continue
		}
		out = append(out, strings.ToUpper(kind))
		for _, r := range rows {
			out = append(out, ctRow(r))
		}
	}
	return append(out, ctRule())
}

func ctBoardFits(out io.Writer, lines []string) bool {
	f, ok := out.(*os.File)
	if !ok {
		return true
	}
	size, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || size.Row == 0 || size.Col == 0 {
		return false
	}
	if len(lines) >= int(size.Row) {
		return false
	}
	for _, line := range lines {
		if len([]rune(line)) > int(size.Col) {
			return false
		}
	}
	return true
}

// Use complete snapshots if some rows have scrolled outside the screen.
// Moving the cursor up in that case would overwrite unrelated rows.
func (b *ctBoard) draw() {
	lines := b.lines()
	if len(lines) == 0 {
		return
	}
	fits := ctBoardFits(b.out, lines)
	if !fits {
		b.inPlace = false
		if slices.Equal(lines, b.shown) || time.Since(b.snapshotAt) < 5*time.Second {
			return
		}
		b.snapshotAt = time.Now()
		b.shown = lines
		_, _ = io.WriteString(b.out, strings.Join(lines, "\n")+"\n")
		return
	}
	var w strings.Builder
	if !b.inPlace || len(b.shown) != len(lines) {
		// First draw, or the row count changed: the region is written out.
		if n := len(b.shown); b.inPlace && n > 0 {
			fmt.Fprintf(&w, "\033[%dF\033[J", n)
		}
		for _, l := range lines {
			w.WriteString(l + "\n")
		}
	} else {
		// The cursor sits under the region; each changed line is reached from
		// there, rewritten, and left.
		for i, l := range lines {
			if l == b.shown[i] {
				continue
			}
			up := len(lines) - i
			fmt.Fprintf(&w, "\033[%dF\033[2K%s\033[%dE", up, l, up)
		}
	}
	b.shown = lines
	b.inPlace = true
	if w.Len() > 0 {
		_, _ = io.WriteString(b.out, w.String())
	}
}

// finish stops the live drawing and sets the verdict out in its place, and
// the recommendation under it.
func (b *ctBoard) finish(results []ConnTestResult, best ConnTestBest) {
	close(b.stop)
	<-b.done
	var w strings.Builder
	if b.tty && b.inPlace && ctBoardFits(b.out, b.shown) && len(b.shown) > 0 {
		fmt.Fprintf(&w, "\033[%dF\033[J", len(b.shown))
	}
	w.WriteString("CONNECTION TEST — " + b.title + "\n" + ctRule() + "\n\n")
	w.WriteString(ConnTestTable(results))
	w.WriteString("\n" + ConnTestBestTable(best))
	_, _ = io.WriteString(b.out, w.String())
}

// abandon stops the live drawing and leaves what is on the screen.
func (b *ctBoard) abandon() {
	close(b.stop)
	<-b.done
}
