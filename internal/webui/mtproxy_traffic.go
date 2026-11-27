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
	mu     sync.RWMutex
	totals map[string]uint64
}

func (m *mtProxyTraffic) fill(rows []TunnelInfo) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range rows {
		if used, ok := m.totals[rows[i].Name]; ok {
			rows[i].MTProxyBytes = &used
		}
	}
}

func (m *mtProxyTraffic) run(ctx context.Context) {
	if _, err := exec.LookPath("nft"); err != nil {
		return
	}
	saved := make(map[string]uint64)
	if err := app.LoadState(mtProxyTrafficFile, &saved); err != nil {
		log.Printf("MTProxy traffic state: %v", err)
		return
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	needCleanup := true
	reportedError := false
	for {
		bindings, active, err := mtProxyTrafficBindings()
		if err == nil && (needCleanup || len(active) > 0) {
			var totals map[string]uint64
			totals, saved, err = updateMTProxyTraffic(ctx, verifiedMTProxyBindings(ctx, bindings, saved), active, saved)
			if err == nil {
				needCleanup = len(active) > 0
				m.mu.Lock()
				m.totals = totals
				m.mu.Unlock()
			}
		}
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

type mtProxyTrafficBinding struct {
	name, counter string
	host          netip.Addr // invalid or unspecified is a wildcard
	port          int
	setup         mtproxy.Setup
	listen        string
}

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
				name: name, counter: counter, host: host.Unmap(), port: port.Public, setup: draft.Setup, listen: port.listen,
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

type mtProxyNFT struct {
	exists   bool
	chains   map[string]bool
	counters map[string]mtProxyCounter
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
	state := mtProxyNFT{chains: make(map[string]bool), counters: make(map[string]mtProxyCounter), rules: make(map[string]int)}
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
	same := state.chains["rx"] && state.chains["tx"] &&
		len(state.rules) == 2*len(bindings) && len(state.counters) == len(bindings)
	for _, b := range bindings {
		have, ok := state.counters[b.counter]
		same = same && ok && have.Bytes >= saved[b.counter] &&
			state.rules[b.ruleID("rx")] == 1 && state.rules[b.ruleID("tx")] == 1
	}
	if same {
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
	for _, b := range bindings {
		if _, ok := state.counters[b.counter]; !ok {
			fmt.Fprintf(&script, "add counter %s %s { packets 0 bytes %d; }\n", mtProxyTable, b.counter, saved[b.counter])
		}
		for _, rule := range []struct{ chain, addrField, portField, direction string }{
			{"rx", "daddr", "dport", "original"}, {"tx", "saddr", "sport", "reply"},
		} {
			fmt.Fprintf(&script, "add rule %s %s ", mtProxyTable, rule.chain)
			if !wildcardMTProxyHost(b.host) {
				family := "ip"
				if b.host.Is6() {
					family = "ip6"
				}
				fmt.Fprintf(&script, "%s %s %s ", family, rule.addrField, b.host)
			}
			fmt.Fprintf(&script, "tcp %s %d ct direction %s counter name %q comment %q\n",
				rule.portField, b.port, rule.direction, b.counter, b.ruleID(rule.chain))
		}
	}
	return script.String()
}

func updateMTProxyTraffic(ctx context.Context, bindings []mtProxyTrafficBinding, active map[string]bool, saved map[string]uint64) (map[string]uint64, map[string]uint64, error) {
	state, err := readMTProxyNFT(ctx)
	if err != nil {
		return nil, saved, err
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
			return nil, saved, fmt.Errorf("update MTProxy counters: %s: %w", strings.TrimSpace(string(out)), err)
		}
		state, err = readMTProxyNFT(ctx)
		if err != nil {
			return nil, saved, err
		}
	}
	totals := make(map[string]uint64)
	for _, b := range bindings {
		if count, ok := state.counters[b.counter]; ok {
			next[b.counter] = max(next[b.counter], count.Bytes)
			totals[b.name] += next[b.counter]
		}
	}
	if !maps.Equal(saved, next) {
		raw, err := json.Marshal(next)
		if err != nil {
			return nil, saved, err
		}
		if err := app.WriteFileAtomic(mtProxyTrafficFile, raw, 0600); err != nil {
			return nil, saved, err
		}
	}
	return totals, next, nil
}
