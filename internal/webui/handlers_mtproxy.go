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
	Host   string        `json:"host"`
	Public int           `json:"public"`
	Setup  mtproxy.Setup `json:"setup"`
}

func (d mtProxyDraft) display(port mtProxyPort) map[string]any {
	out := map[string]any{
		"address": d.Host, "public": d.Public, "destination": d.Setup.Port,
		"link": d.Setup.Link(d.Host, d.Public), "command": d.Setup.Command(),
		"remove": "sudo backpack mtproxy remove " + d.Setup.ID(),
		"bind":   d.Setup.BindIP(), "engine": "Telemt " + mtproxy.Version,
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
		out := map[string]any{"ports": ports, "host": panelHost(r), "version": mtproxy.Version, "note": note}
		if saved {
			out["proxy"] = draft.display(current)
		}
		writeJSON(w, out)
	case http.MethodPost:
		var req struct {
			Action  string `json:"action"`
			Host    string `json:"host"`
			Domain  string `json:"domain"`
			Port    int    `json:"port"`
			WebIP   string `json:"webIp"`
			Sponsor string `json:"sponsor"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Action {
		case "create":
			if saved {
				writeJSON(w, draft.display(current))
				return
			}
			port, err := chooseMTProxyPort(ports, req.Port)
			host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Host), "."))
			if err != nil || !mtproxy.ValidPublicHost(host) {
				http.Error(w, "choose a forwarded TCP port and enter the public address", http.StatusBadRequest)
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
			saveMTProxy(w, path, mtProxyDraft{Host: host, Public: port.Public, Setup: setup}, port)
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
		case "clear":
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
	data, err := json.Marshal(draft)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0700)
	}
	if err == nil {
		err = app.WriteFileAtomic(path, data, 0600)
	}
	if err != nil {
		http.Error(w, "could not save the MTProxy setup", http.StatusInternalServerError)
		return
	}
	writeJSON(w, draft.display(port))
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
