package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/mtproxy"
	"github.com/backpack/backpack/internal/utils/network"
)

type mtProxyPort struct {
	Public      int `json:"public"`
	Destination int `json:"destination"`
	listen      string
}

// The trial uses existing TCP mappings to the peer's IPv4 loopback. Binding
// mtg on this end or forwarding a UDP-only rule cannot serve Telegram.
func mtProxyEligible(t manage.Tunnel) bool {
	if t.Role != "server" || t.Unrunnable != "" {
		return false
	}
	switch t.Transport {
	case "tcp", "tcpmux", "ws", "wss", "wsmux", "wssmux", "pck", "quic", "gost", "anytls",
		"native/xtcpmux", "native/xwsmux", "native/dagger-http", "native/dagger-https", "native/dagger-xhttp":
		return true
	}
	return false
}

func mtProxyPorts(t manage.Tunnel) []mtProxyPort {
	out := make([]mtProxyPort, 0)
	for _, rule := range t.Ports {
		bind, target, hasTarget := strings.Cut(rule, "=")
		bind, target = strings.TrimSpace(bind), strings.TrimSpace(target)
		ip, port := "127.0.0.1", bind
		if host, p, err := net.SplitHostPort(bind); err == nil {
			parsed := net.ParseIP(host)
			if parsed == nil || parsed.IsLoopback() {
				continue
			}
			ip, port = host, p
			if parsed.IsUnspecified() {
				ip = "127.0.0.1"
				if parsed.To4() == nil {
					ip = "::1"
				}
			}
		}
		public, err := strconv.Atoi(port)
		if err != nil || public < 1 || public > 65535 {
			continue
		}
		if !hasTarget {
			target = port
		}
		destination, endpoint, err := network.ResolveRemoteAddr(target)
		if err != nil || destination < 1 || destination > 65535 ||
			endpoint != net.JoinHostPort("127.0.0.1", strconv.Itoa(destination)) {
			continue
		}
		out = append(out, mtProxyPort{
			Public: public, Destination: destination, listen: net.JoinHostPort(ip, port),
		})
	}
	return out
}

type mtProxyDraft struct {
	Host   string        `json:"host"`
	Public int           `json:"public"`
	Setup  mtproxy.Setup `json:"setup"`
}

func (d mtProxyDraft) display() map[string]any {
	return map[string]any{
		"address":     d.Host,
		"public":      d.Public,
		"destination": d.Setup.Port,
		"link":        d.Setup.Link(d.Host, d.Public),
		"command":     d.Setup.Command(),
		"remove":      "sudo backpack mtproxy remove " + d.Setup.ID(),
	}
}

var mtProxyMu sync.Mutex

func (s *server) handleMTProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := r.URL.Query().Get("name")
	if err := core.CheckName(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, ok := manage.Find(name)
	if !ok || !mtProxyEligible(t) {
		http.Error(w, "choose a reverse tunnel that forwards TCP ports", http.StatusBadRequest)
		return
	}
	cfg, err := core.LoadTunnelConfig(name)
	if err != nil {
		http.Error(w, "could not read the tunnel configuration", http.StatusConflict)
		return
	}
	ports := mtProxyPorts(t)
	var note string
	if cfg.Server.ProxyProtocol || (cfg.Backhaul.Mux != nil && cfg.Backhaul.Mux.ProxyProtocol) {
		ports = nil
		note = "This trial requires a tunnel with PROXY protocol disabled."
	}
	path := filepath.Join(app.ConfigDir, "mtproxy-panel", name+".json")
	mtProxyMu.Lock()
	locked := true
	defer func() {
		if locked {
			mtProxyMu.Unlock()
		}
	}()
	var draft mtProxyDraft
	err = app.LoadState(path, &draft)
	if err != nil {
		http.Error(w, "could not read the saved MTProxy setup", http.StatusInternalServerError)
		return
	}
	saved := draft.Setup.Validate() == nil && mtproxy.ValidPublicHost(draft.Host)

	switch r.Method {
	case http.MethodGet:
		locked = false
		mtProxyMu.Unlock()
		out := map[string]any{"ports": ports, "host": panelHost(r), "version": mtproxy.Version, "note": note}
		if saved {
			out["proxy"] = draft.display()
		}
		writeJSON(w, out)
	case http.MethodPost:
		var req struct {
			Action string `json:"action"`
			Host   string `json:"host"`
			Domain string `json:"domain"`
			Port   int    `json:"port"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Action {
		case "create":
			if saved {
				writeJSON(w, draft.display())
				return
			}
			port, err := chooseMTProxyPort(ports, req.Port)
			host := strings.ToLower(strings.TrimSpace(req.Host))
			if err != nil || !mtproxy.ValidPublicHost(host) {
				http.Error(w, "choose an available forwarded TCP port and enter the public IP or hostname", http.StatusBadRequest)
				return
			}
			if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(req.Domain), "."), host) {
				http.Error(w, "choose a TLS hostname distinct from the public proxy address", http.StatusBadRequest)
				return
			}
			setup, err := mtproxy.NewSetup(port.Destination, req.Domain)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			draft = mtProxyDraft{Host: host, Public: port.Public, Setup: setup}
			data, err := json.Marshal(draft)
			if err != nil {
				http.Error(w, "could not encode the MTProxy setup", http.StatusInternalServerError)
				return
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
				err = app.WriteFileAtomic(path, data, 0600)
				if err == nil {
					writeJSON(w, draft.display())
					return
				}
			}
			http.Error(w, "could not save the MTProxy setup", http.StatusInternalServerError)
		case "check":
			port, err := chooseMTProxyPort(ports, draft.Public)
			if !saved || err != nil || port.Destination != draft.Setup.Port {
				http.Error(w, "the saved TCP forwarding rule is unavailable", http.StatusConflict)
				return
			}
			locked = false
			mtProxyMu.Unlock()
			elapsed, err := draft.Setup.CheckPath(r.Context(), port.listen)
			if err != nil {
				http.Error(w, fmt.Sprintf("Could not authenticate MTProxy through the forwarded port. Check the exit service and setup. (%s)", err), http.StatusBadGateway)
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

func chooseMTProxyPort(ports []mtProxyPort, port int) (mtProxyPort, error) {
	var selected mtProxyPort
	for _, p := range ports {
		if p.Public == port {
			if selected.Public != 0 {
				return mtProxyPort{}, errors.New("the forwarding port is configured on multiple addresses")
			}
			selected = p
		}
	}
	if selected.Public == 0 {
		return mtProxyPort{}, errors.New("the forwarding port is not configured for MTProxy")
	}
	return selected, nil
}
