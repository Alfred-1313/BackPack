package webui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/mtproxy"
)

const mtProxyTable = "inet backpack_mtproxy"
const mtProxyTrafficFile = app.ConfigDir + "/mtproxy-traffic.json"

type mtProxyTraffic struct {
	mu          sync.RWMutex
	usage       map[string][]mtProxyUsage
	reconcileMu sync.Mutex
}

type mtProxyUsage struct {
	ID    string `json:"id"`
	Mode  string `json:"mode"`
	Label string `json:"label"`
	Used  uint64 `json:"used"`
	Limit uint64 `json:"limit"`
	Hit   bool   `json:"hit"`
}

func (m *mtProxyTraffic) status(name string, draft mtProxyDraft, mode string) mtProxyUsage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := mtProxyUsage{ID: draft.Setup.ID(), Mode: mode, Label: draft.label(mode), Limit: draft.Limit}
	for _, current := range m.usage[name] {
		if current.ID == status.ID {
			status.Used = current.Used
			status.Hit = status.Limit != 0 && (status.Used >= status.Limit || current.Limit == status.Limit && current.Hit)
			break
		}
	}
	return status
}

func (m *mtProxyTraffic) fill(rows []TunnelInfo) {
	m.mu.RLock()
	usage := m.usage
	m.mu.RUnlock()
	if len(usage) == 0 || len(rows) == 0 {
		return
	}

	// The accounting worker owns reconciliation and disk persistence. Reading
	// the kernel here gives every browser the same live counters without writes.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	state, _ := readMTProxyNFT(ctx)
	for i := range rows {
		if proxies := usage[rows[i].Name]; len(proxies) != 0 {
			var total uint64
			for _, proxy := range proxies {
				proxy.Used = max(proxy.Used, state.counters["p_"+proxy.ID].Bytes)
				if quota := state.quotas["q_"+proxy.ID]; proxy.Limit != 0 {
					proxy.Hit = proxy.Hit || proxy.Used >= proxy.Limit ||
						quota.Bytes == proxy.Limit && quota.Used >= proxy.Limit
				}
				total += proxy.Used
				rows[i].MTProxyUsage = append(rows[i].MTProxyUsage, proxy)
			}
			rows[i].MTProxyBytes = &total
		}
	}
}

func (m *mtProxyTraffic) run(ctx context.Context) {
	if _, err := exec.LookPath("nft"); err != nil {
		return
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	reportedError := false
	for {
		err := m.refresh(ctx)
		if err != nil && !reportedError && ctx.Err() == nil {
			log.Printf("MTProxy traffic accounting: %v", err)
		}
		reportedError = err != nil
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (m *mtProxyTraffic) refresh(ctx context.Context) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	saved := make(map[string]uint64)
	if err := app.LoadState(mtProxyTrafficFile, &saved); err != nil {
		return err
	}
	bindings, active, err := mtProxyTrafficBindings()
	if err != nil {
		return err
	}
	usage, err := updateMTProxyTraffic(ctx, verifiedMTProxyBindings(ctx, bindings, saved), active, saved)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.usage = usage
	m.mu.Unlock()
	return nil
}

type mtProxyTrafficBinding struct {
	name, counter string
	mode, label   string
	limit         uint64
	host          netip.Addr // invalid or unspecified is a wildcard
	port          int
	setup         mtproxy.Setup
	listen        string
}

func (b mtProxyTrafficBinding) quotaName() string { return "q_" + strings.TrimPrefix(b.counter, "p_") }

func (b mtProxyTrafficBinding) ruleID(direction string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", b.counter, b.host, b.port))
	return fmt.Sprintf("%s_%x", direction, digest[:8])
}

func mtProxyTrafficBindings() ([]mtProxyTrafficBinding, map[string]bool, error) {
	active := make(map[string]bool)
	var bindings []mtProxyTrafficBinding
	var tunnels map[string]manage.Tunnel
	for _, mode := range []string{mtproxy.ModeNative, mtproxy.ModeWeb} {
		dir := mtProxyModeDir(mode)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			name, ok := strings.CutSuffix(entry.Name(), ".json")
			if !ok || !entry.Type().IsRegular() || core.CheckName(name) != nil {
				continue
			}
			var draft mtProxyDraft
			if err := app.LoadState(filepath.Join(dir, entry.Name()), &draft); err != nil {
				return nil, nil, err
			}
			if draft.Setup.Validate() != nil || (mode == mtproxy.ModeWeb) != (draft.Setup.Mode == mtproxy.ModeWeb) {
				continue
			}
			counter := "p_" + draft.Setup.ID()
			active[counter] = true
			if tunnels == nil {
				tunnels = make(map[string]manage.Tunnel)
				for _, tunnel := range manage.List() {
					tunnels[tunnel.Name] = tunnel
				}
			}
			t, ok := tunnels[name]
			if !ok || !mtProxyEligible(t) {
				continue
			}
			cfg, err := core.LoadTunnelConfig(t.Name)
			if err != nil || mtProxyHasProxyProtocol(cfg) {
				continue
			}
			exit, err := mtProxyExit(cfg)
			if err != nil {
				continue
			}
			port, err := chooseMTProxyPort(mtProxyPorts(t, exit, mode), draft.Public)
			if err != nil || !draft.matches(port) {
				continue
			}
			host, _ := netip.ParseAddr(port.ingress)
			bindings = append(bindings, mtProxyTrafficBinding{
				name: name, counter: counter, mode: mode, label: draft.label(mode), limit: draft.Limit,
				host: host.Unmap(), port: port.Public, setup: draft.Setup, listen: port.listen,
			})
		}
	}
	// A shared or overlapping listener cannot be attributed to one draft.
	var resolved []mtProxyTrafficBinding
	for i, b := range bindings {
		unique := true
		for j, other := range bindings {
			if i != j && (b.counter == other.counter || (b.port == other.port &&
				(wildcardMTProxyHost(b.host) || wildcardMTProxyHost(other.host) || b.host == other.host))) {
				unique = false
				break
			}
		}
		if unique {
			resolved = append(resolved, b)
		}
	}
	return resolved, active, nil
}

// A saved setup may reuse an existing forward before the proxy is started.
// Confirm the actual service before attributing that port's traffic to it.
func verifiedMTProxyBindings(ctx context.Context, bindings []mtProxyTrafficBinding, saved map[string]uint64) []mtProxyTrafficBinding {
	ready := bindings[:0]
	for _, b := range bindings {
		if _, verified := saved[b.counter]; verified {
			ready = append(ready, b)
			continue
		}
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := b.setup.CheckListener(probe, b.listen)
		cancel()
		if err == nil {
			ready = append(ready, b)
		}
	}
	return ready
}

func wildcardMTProxyHost(host netip.Addr) bool { return !host.IsValid() || host.IsUnspecified() }

type mtProxyCounter struct {
	Name   string
	Bytes  uint64
	Handle uint64
}

type mtProxyNFTQuota struct {
	Name   string
	Bytes  uint64
	Used   uint64
	Handle uint64
}

type mtProxyNFT struct {
	exists   bool
	chains   map[string]bool
	counters map[string]mtProxyCounter
	quotas   map[string]mtProxyNFTQuota
	rules    map[string]int
}

func mtProxyNFTCommand(ctx context.Context, script string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nft", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if script != "" {
		cmd.Stdin = strings.NewReader(script)
	}
	return cmd.CombinedOutput()
}

func readMTProxyNFT(ctx context.Context) (mtProxyNFT, error) {
	state := mtProxyNFT{chains: make(map[string]bool), counters: make(map[string]mtProxyCounter), quotas: make(map[string]mtProxyNFTQuota), rules: make(map[string]int)}
	out, err := mtProxyNFTCommand(ctx, "", "-j", "-a", "list", "table", "inet", "backpack_mtproxy")
	if err != nil {
		if !strings.Contains(string(out), "No such file or directory") {
			return state, fmt.Errorf("read MTProxy counters: %w", err)
		}
		return state, nil
	}
	var document struct {
		Entries []struct {
			Table   *struct{}                 `json:"table"`
			Chain   *struct{ Name string }    `json:"chain"`
			Counter *mtProxyCounter           `json:"counter"`
			Quota   *mtProxyNFTQuota          `json:"quota"`
			Rule    *struct{ Comment string } `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &document); err != nil {
		return state, err
	}
	for _, item := range document.Entries {
		switch {
		case item.Table != nil:
			state.exists = true
		case item.Chain != nil:
			state.chains[item.Chain.Name] = true
		case item.Counter != nil:
			state.counters[item.Counter.Name] = *item.Counter
		case item.Quota != nil:
			state.quotas[item.Quota.Name] = *item.Quota
		case item.Rule != nil:
			state.rules[item.Rule.Comment]++
		}
	}
	return state, nil
}

func (state mtProxyNFT) reconcile(bindings []mtProxyTrafficBinding, saved map[string]uint64) string {
	if len(bindings) == 0 {
		if state.exists {
			return "delete table " + mtProxyTable + "\n"
		}
		return ""
	}
	same := state.chains["rx"] && state.chains["tx"] && len(state.counters) == len(bindings)
	wantQuotas := make(map[string]uint64)
	for _, b := range bindings {
		have, ok := state.counters[b.counter]
		same = same && ok && have.Bytes >= saved[b.counter] &&
			state.rules[b.ruleID("rx")] == 1 && state.rules[b.ruleID("tx")] == 1
		if b.limit != 0 {
			wantQuotas[b.quotaName()] = b.limit
			same = same && state.quotas[b.quotaName()].Bytes == b.limit &&
				state.rules[b.ruleID("limit_rx")] == 1 && state.rules[b.ruleID("limit_tx")] == 1
		}
	}
	if same && len(state.quotas) == len(wantQuotas) && len(state.rules) == 2*(len(bindings)+len(wantQuotas)) {
		return ""
	}
	var script strings.Builder
	if !state.exists {
		fmt.Fprintf(&script, "add table %s\n", mtProxyTable)
	}
	for _, direction := range []struct{ chain, hook string }{{"rx", "input"}, {"tx", "output"}} {
		if state.chains[direction.chain] {
			fmt.Fprintf(&script, "flush chain %s %s\n", mtProxyTable, direction.chain)
		} else {
			fmt.Fprintf(&script, "add chain %s %s { type filter hook %s priority 10; policy accept; }\n", mtProxyTable, direction.chain, direction.hook)
		}
	}
	want := make(map[string]bool, len(bindings))
	for _, b := range bindings {
		want[b.counter] = true
	}
	for name, counter := range state.counters {
		if !want[name] || counter.Bytes < saved[name] {
			fmt.Fprintf(&script, "delete counter %s handle %d\n", mtProxyTable, counter.Handle)
			delete(state.counters, name)
		}
	}
	for name, quota := range state.quotas {
		if wantQuotas[name] != quota.Bytes {
			fmt.Fprintf(&script, "delete quota %s handle %d\n", mtProxyTable, quota.Handle)
			delete(state.quotas, name)
		}
	}
	for _, b := range bindings {
		if b.limit != 0 && state.quotas[b.quotaName()].Bytes == 0 {
			fmt.Fprintf(&script, "add quota %s %s { over %d bytes used %d bytes; }\n",
				mtProxyTable, b.quotaName(), b.limit, min(b.limit, saved[b.counter]))
		}
		if _, ok := state.counters[b.counter]; !ok {
			fmt.Fprintf(&script, "add counter %s %s { packets 0 bytes %d; }\n", mtProxyTable, b.counter, saved[b.counter])
		}
		for _, rule := range []struct{ chain, addrField, portField, direction string }{
			{"rx", "daddr", "dport", "original"}, {"tx", "saddr", "sport", "reply"},
		} {
			var match strings.Builder
			fmt.Fprintf(&match, "add rule %s %s ", mtProxyTable, rule.chain)
			if !wildcardMTProxyHost(b.host) {
				family := "ip"
				if b.host.Is6() {
					family = "ip6"
				}
				fmt.Fprintf(&match, "%s %s %s ", family, rule.addrField, b.host)
			}
			fmt.Fprintf(&match, "tcp %s %d ct direction %s ", rule.portField, b.port, rule.direction)
			// Both directions draw from the same budget. Count only packets let through.
			if b.limit != 0 {
				fmt.Fprintf(&script, "%squota name %q drop comment %q\n",
					match.String(), b.quotaName(), b.ruleID("limit_"+rule.chain))
			}
			fmt.Fprintf(&script, "%scounter name %q comment %q\n", match.String(), b.counter, b.ruleID(rule.chain))
		}
	}
	return script.String()
}

func updateMTProxyTraffic(ctx context.Context, bindings []mtProxyTrafficBinding, active map[string]bool, saved map[string]uint64) (map[string][]mtProxyUsage, error) {
	state, err := readMTProxyNFT(ctx)
	if err != nil {
		return nil, err
	}
	next := make(map[string]uint64, len(active))
	for name := range active {
		if total, ok := saved[name]; ok {
			next[name] = total
		}
		if counter, ok := state.counters[name]; ok {
			next[name] = max(next[name], counter.Bytes)
		}
	}
	if script := state.reconcile(bindings, next); script != "" {
		if out, err := mtProxyNFTCommand(ctx, script, "-f", "-"); err != nil {
			return nil, fmt.Errorf("update MTProxy counters: %s: %w", strings.TrimSpace(string(out)), err)
		}
		state, err = readMTProxyNFT(ctx)
		if err != nil {
			return nil, err
		}
	}
	usage := make(map[string][]mtProxyUsage)
	for _, b := range bindings {
		if count, ok := state.counters[b.counter]; ok {
			next[b.counter] = max(next[b.counter], count.Bytes)
			usage[b.name] = append(usage[b.name], mtProxyUsage{
				ID: b.setup.ID(), Mode: b.mode, Label: b.label, Used: next[b.counter], Limit: b.limit,
				Hit: b.limit != 0 && (count.Bytes >= b.limit || state.quotas[b.quotaName()].Used >= b.limit),
			})
		}
	}
	if !maps.Equal(saved, next) {
		raw, err := json.Marshal(next)
		if err != nil {
			return nil, err
		}
		if err := app.WriteFileAtomic(mtProxyTrafficFile, raw, 0600); err != nil {
			return nil, err
		}
	}
	return usage, nil
}
