package webui

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/mtproxy"
	"github.com/backpack/backpack/internal/node"
	"github.com/backpack/backpack/internal/tunnel/l3"
	"github.com/backpack/backpack/internal/tunnel/portmap"
)

type mtProxyPort struct {
	Public      int    `json:"public"`
	Destination int    `json:"destination"`
	Bind        string `json:"bind"`
	listen      string
}

var mtProxyReverseCarriers = []string{
	"tcp", "tcpmux", "ws", "wss", "wsmux", "wssmux", "pck", "quic", "kcp", "gost", "anytls",
	"native/xtcpmux", "native/xwsmux", "native/dagger-http", "native/dagger-https", "native/dagger-xhttp",
}

func mtProxyEligible(t manage.Tunnel) bool {
	if t.Unrunnable != "" {
		return false
	}
	if t.Role == "iran" && strings.HasPrefix(t.Transport, "l3/") {
		return true
	}
	if t.Role != "server" {
		return false
	}
	for _, carrier := range mtProxyReverseCarriers {
		if carrier == t.Transport {
			return true
		}
	}
	return false
}

func mtProxyPorts(t manage.Tunnel, exit, mode string) []mtProxyPort {
	out := make([]mtProxyPort, 0)
	exitIP, err := netip.ParseAddr(exit)
	if err != nil || !(exitIP.IsPrivate() || exitIP.IsLoopback()) ||
		(mode == mtproxy.ModeWeb && exit != "127.0.0.1") {
		return out
	}
	for _, rule := range t.Ports {
		mappings, err := portmap.Expand([]string{rule}, exit)
		if err != nil {
			continue
		}
		for _, m := range mappings {
			if len(m.Targets) != 1 {
				continue
			}
			bind, dest, err := net.SplitHostPort(m.Targets[0])
			if err != nil || bind != exit {
				continue
			}
			host, port, err := net.SplitHostPort(m.Listen)
			if err != nil || (host != "" && net.ParseIP(host) == nil) {
				continue
			}
			if (mode == mtproxy.ModeWeb) != net.ParseIP(host).IsLoopback() {
				continue
			}
			if host == "" || host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			if host == "::" {
				host = "::1"
			}
			public, _ := strconv.Atoi(port)
			destination, _ := strconv.Atoi(dest)
			out = append(out, mtProxyPort{Public: public, Destination: destination,
				Bind: bind, listen: net.JoinHostPort(host, port)})
		}
	}
	return out
}

type mtProxyDraft struct {
	Host    string        `json:"host"`
	Public  int           `json:"public"`
	Setup   mtproxy.Setup `json:"setup"`
	Forward string        `json:"forward,omitempty"`
}

func (d mtProxyDraft) display(port mtProxyPort) map[string]any {
	out := map[string]any{
		"address": d.Host, "public": d.Public, "destination": d.Setup.Port,
		"link": d.Setup.Link(d.Host, d.Public), "command": d.Setup.Command(),
		"remove": "sudo backpack mtproxy remove " + d.Setup.ID(),
		"bind":   d.Setup.BindIP(), "engine": "Telemt " + mtproxy.Version,
		"ownsForward": d.Forward != "",
	}
	if d.Setup.Mode == "" {
		out["engine"] = "mtg " + mtproxy.LegacyVersion
	}
	if d.Setup.Mode == mtproxy.ModeWeb {
		if d.matches(port) {
			out["frontend"] = d.Setup.TLSFrontend(port.listen)
		}
	} else {
		out["registrationSecret"] = d.Setup.RegistrationSecret()
		out["sponsor"] = d.Setup.Sponsor
	}
	return out
}

func (d mtProxyDraft) matches(p mtProxyPort) bool {
	return p.Public == d.Public && p.Destination == d.Setup.Port && p.Bind == d.Setup.BindIP()
}

var mtProxyMu sync.Mutex

func (s *server) handleMTProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name, mode := r.URL.Query().Get("name"), r.URL.Query().Get("mode")
	if mode == "" {
		mode = mtproxy.ModeNative
	}
	if mode != mtproxy.ModeNative && mode != mtproxy.ModeWeb {
		http.Error(w, "unknown MTProxy mode", http.StatusBadRequest)
		return
	}
	if err := core.CheckName(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, ok := manage.Find(name)
	if !ok || !mtProxyEligible(t) {
		http.Error(w, "choose a tunnel that forwards TCP ports", http.StatusBadRequest)
		return
	}
	cfg, err := core.LoadTunnelConfig(name)
	if err != nil {
		http.Error(w, "could not read the tunnel configuration", http.StatusConflict)
		return
	}
	exit := "127.0.0.1"
	if cfg.L3.Enabled() && cfg.L3.Carrier != l3.CarrierPck {
		layer, err := l3.ConfigFromTable(cfg.L3)
		if err != nil {
			http.Error(w, "could not read the private tunnel address", http.StatusConflict)
			return
		}
		exit = layer.PeerIP
	}
	ports := mtProxyPorts(t, exit, mode)
	var note string
	if mode == mtproxy.ModeWeb && exit != "127.0.0.1" {
		note = "WEB requires a loopback TCP mapping on both servers. Use a reverse stream tunnel or direct PCK."
	}
	if cfg.Server.ProxyProtocol || (cfg.Backhaul.Mux != nil && cfg.Backhaul.Mux.ProxyProtocol) {
		ports = nil
		note = "Choose a tunnel with PROXY protocol disabled."
	}

	pair, paired := manage.PairFor(name)
	run := s.nodes.Runner()
	managed := paired && run != nil
	udp := (cfg.Server.BindAddr != "" && cfg.Server.ForwardsUDP()) || cfg.L3.AcceptUDP
	plan := func(public, destination int) (mtProxyPort, string, error) {
		if note != "" {
			return mtProxyPort{}, "", fmt.Errorf("%s", note)
		}
		return planMTProxyForward(t, ports, public, destination, exit, mode, udp)
	}

	path := filepath.Join(app.ConfigDir, "mtproxy-panel")
	if mode == mtproxy.ModeWeb {
		path = filepath.Join(path, "web")
	}
	path = filepath.Join(path, name+".json")
	mtProxyMu.Lock()
	locked := true
	defer func() {
		if locked {
			mtProxyMu.Unlock()
		}
	}()
	var draft mtProxyDraft
	if err := app.LoadState(path, &draft); err != nil {
		http.Error(w, "could not read the saved MTProxy setup", http.StatusInternalServerError)
		return
	}
	saved := draft.Setup.Validate() == nil && mtproxy.ValidPublicHost(draft.Host)
	current, _ := chooseMTProxyPort(ports, draft.Public)

	switch r.Method {
	case http.MethodGet:
		locked = false
		mtProxyMu.Unlock()
		out := map[string]any{"ports": ports, "host": panelHost(r), "version": mtproxy.Version, "note": note,
			"bind": exit, "managed": managed, "active": manage.IsActive(t.Service)}
		if saved {
			out["proxy"] = draft.display(current)
		}
		writeJSON(w, out)
	case http.MethodPost:
		var req struct {
			Action      string `json:"action"`
			Host        string `json:"host"`
			Domain      string `json:"domain"`
			Port        int    `json:"port"`
			WebIP       string `json:"webIp"`
			Sponsor     string `json:"sponsor"`
			Destination int    `json:"destination"`
			PortResult  string `json:"portResult"`
			Confirmed   bool   `json:"confirmed"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Action {
		case "check-ports":
			locked = false
			mtProxyMu.Unlock()
			errors := map[string]string{}
			if _, _, err := plan(req.Port, req.Destination); req.Port != 0 && err != nil {
				errors["public"] = err.Error()
			}
			if managed && req.Destination != 0 {
				check := mtproxy.PortCheck{Bind: exit, Port: req.Destination}
				var status mtproxy.PortStatus
				if err := run.Call(pair.Node, node.OpMTProxyPort, check, &status); err != nil {
					errors["destination"] = err.Error()
				} else if status.Busy {
					errors["destination"] = mtProxyBusy(status.Port)
				}
			}
			writeJSON(w, errors)
		case "random-public":
			for i := 0; i < 16; i++ {
				p := manage.SuggestPort()
				if p == 0 {
					break
				}
				if _, _, err := plan(p, req.Destination); err == nil {
					writeJSON(w, map[string]int{"port": p})
					return
				}
			}
			http.Error(w, "could not find a free port on Iran", http.StatusConflict)
		case "random-destination":
			locked = false
			mtProxyMu.Unlock()
			if !managed {
				http.Error(w, "run the random port command on Kharej", http.StatusConflict)
				return
			}
			var status mtproxy.PortStatus
			if err := run.Call(pair.Node, node.OpMTProxyPort, mtproxy.PortCheck{Bind: exit}, &status); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			writeJSON(w, status)
		case "create":
			if saved {
				writeJSON(w, draft.display(current))
				return
			}
			port, forward, err := plan(req.Port, req.Destination)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Host), "."))
			if !mtproxy.ValidPublicHost(host) {
				http.Error(w, "enter the public address", http.StatusBadRequest)
				return
			}
			check := mtproxy.PortCheck{Bind: port.Bind, Port: port.Destination}
			if managed {
				var status mtproxy.PortStatus
				if err := run.Call(pair.Node, node.OpMTProxyPort, check, &status); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				if status.Busy {
					http.Error(w, mtProxyBusy(status.Port), http.StatusConflict)
					return
				}
			} else if !check.ManualResult(req.PortResult) {
				http.Error(w, "check the destination port on Kharej and paste its FREE result", http.StatusBadRequest)
				return
			}
			domain := req.Domain
			if mode == mtproxy.ModeWeb {
				domain = host
			} else if port.Public == 443 && strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(domain), "."), host) {
				http.Error(w, "choose a TLS hostname that will not forward back to this proxy", http.StatusBadRequest)
				return
			}
			setup, err := mtproxy.NewProxy(mode, port.Destination, port.Bind, domain, req.WebIP, req.Sponsor)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			draft = mtProxyDraft{Host: host, Public: port.Public, Setup: setup, Forward: forward}
			if err := writeMTProxyDraft(path, draft); err != nil {
				http.Error(w, "could not save the MTProxy setup", http.StatusInternalServerError)
				return
			}
			if forward != "" {
				if err := manage.ApplyMTProxyForward(t, forward, false); err != nil {
					_ = os.Remove(path)
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
			}
			writeJSON(w, draft.display(port))
		case "sponsor":
			tag := strings.ToLower(strings.TrimSpace(req.Sponsor))
			if mode != mtproxy.ModeNative || !saved || (tag != "" && !mtproxy.ValidTag(tag)) {
				http.Error(w, "enter the sponsor tag from @MTProxybot", http.StatusBadRequest)
				return
			}
			draft.Setup.Mode, draft.Setup.Sponsor = mtproxy.ModeNative, tag
			saveMTProxy(w, path, draft, current)
		case "check":
			if !saved || !draft.matches(current) {
				http.Error(w, "the saved TCP forwarding rule is unavailable", http.StatusConflict)
				return
			}
			locked = false
			mtProxyMu.Unlock()
			var elapsed time.Duration
			var err error
			if mode == mtproxy.ModeWeb {
				elapsed, err = draft.Setup.CheckWeb(r.Context(), current.listen)
			} else {
				elapsed, err = draft.Setup.CheckPath(r.Context(), current.listen)
			}
			if err != nil {
				http.Error(w, fmt.Sprintf("MTProxy check failed. %s", err), http.StatusBadGateway)
				return
			}
			writeJSON(w, map[string]int64{"ms": elapsed.Milliseconds()})
		case "remove", "clear":
			if saved && managed {
				if err := run.Call(pair.Node, node.OpMTProxyRemove, draft.Setup.ID(), nil); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
			} else if saved && !req.Confirmed {
				http.Error(w, "run the removal command on Kharej first", http.StatusBadRequest)
				return
			}
			if saved && draft.Forward != "" {
				if err := manage.ApplyMTProxyForward(t, draft.Forward, true); err != nil {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				http.Error(w, "could not remove the saved MTProxy setup", http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]bool{"cleared": true})
		default:
			http.Error(w, "unknown MTProxy action", http.StatusBadRequest)
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func saveMTProxy(w http.ResponseWriter, path string, draft mtProxyDraft, port mtProxyPort) {
	if err := writeMTProxyDraft(path, draft); err != nil {
		http.Error(w, "could not save the MTProxy setup", http.StatusInternalServerError)
		return
	}
	writeJSON(w, draft.display(port))
}

func writeMTProxyDraft(path string, draft mtProxyDraft) error {
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return app.WriteFileAtomic(path, data, 0600)
}

func mtProxyBusy(port int) string {
	return fmt.Sprintf("Port %d (TCP) is already in use on Kharej.", port)
}

func planMTProxyForward(t manage.Tunnel, ports []mtProxyPort, public, destination int, exit, mode string, udp bool) (mtProxyPort, string, error) {
	if public < 1 || public > 65535 || destination < 0 || destination > 65535 {
		return mtProxyPort{}, "", fmt.Errorf("enter port numbers from 1 to 65535")
	}
	existing, err := chooseMTProxyPort(ports, public)
	if err == nil && (destination == 0 || destination == existing.Destination) {
		return existing, "", nil
	}
	host := ""
	if mode == mtproxy.ModeWeb {
		host = "127.0.0.1"
	}
	if destination == 0 {
		destination = public
	}
	listen := net.JoinHostPort(host, strconv.Itoa(public))
	for _, rule := range t.Ports {
		left, _, _ := strings.Cut(rule, "=")
		mappings, _ := portmap.Expand([]string{left}, exit)
		for _, mapping := range mappings {
			other, port, _ := net.SplitHostPort(mapping.Listen)
			if port == strconv.Itoa(public) && (other == host || host == "" || other == "" || other == "0.0.0.0" || other == "::") {
				return mtProxyPort{}, "", fmt.Errorf("Port %d already forwards to a service on this tunnel. Choose a separate port on Iran.", public)
			}
		}
	}
	left := strings.TrimPrefix(listen, ":")
	control := ""
	if t.Role == "server" {
		control = t.Addr
	}
	check := manage.CheckForwardPorts(manage.ForwardPortCheck{
		Ports: left, TCP: true, UDP: udp, TunnelPort: control, TunnelTransport: t.Transport,
	})
	if check.Ports != "" {
		return mtProxyPort{}, "", fmt.Errorf("%s", check.Ports)
	}
	rule := left + "=" + net.JoinHostPort(exit, strconv.Itoa(destination))
	return mtProxyPort{Public: public, Destination: destination, Bind: exit, listen: net.JoinHostPort("127.0.0.1", strconv.Itoa(public))}, rule, nil
}

func chooseMTProxyPort(ports []mtProxyPort, port int) (mtProxyPort, error) {
	var selected mtProxyPort
	for _, p := range ports {
		if p.Public != port {
			continue
		}
		if selected.Public != 0 {
			return mtProxyPort{}, fmt.Errorf("the forwarding port is configured on multiple addresses")
		}
		selected = p
	}
	if selected.Public == 0 {
		return mtProxyPort{}, fmt.Errorf("the forwarding port is not configured for MTProxy")
	}
	return selected, nil
}
