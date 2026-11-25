package webui

import (
	"bytes"
	"github.com/backpack/backpack/internal/gostdirect"
	"net/http"
	"strings"

	"github.com/backpack/backpack/internal/daggerconnect"
	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/nativebackhaul"
	"github.com/backpack/backpack/internal/omni"
)

func (s *server) handleGostDirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var f gostdirect.Form
	if err := decodeJSON(w, r, &f); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	res, err := gostdirect.Install(r.Context(), f)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, res)
}

// Keep the same argument validation and pinned executable as the CLI.
func (s *server) handleOmni(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		writeJSON(w, omni.Options())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var form struct {
		Args  []string `json:"args"`
		Token string   `json:"token"`
	}
	if err := decodeJSON(w, r, &form); err != nil || len(form.Args) == 0 || len(form.Args) > 6 {
		http.Error(w, "invalid OmniTunnel operation", http.StatusBadRequest)
		return
	}
	var out bytes.Buffer
	if err := omni.Run(form.Args, strings.NewReader(form.Token), &out, &out); err != nil {
		http.Error(w, strings.TrimSpace(out.String()+"\n"+err.Error()), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"output": out.String()})
}

func (s *server) handleDagger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, daggerconnect.Options())
	case http.MethodPost:
		var form manage.DaggerForm
		if err := decodeJSON(w, r, &form); err != nil {
			http.Error(w, "invalid DaggerConnect form", http.StatusBadRequest)
			return
		}
		out, err := manage.CreateDagger(r.Context(), form)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, out)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleBackhaul(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, nativebackhaul.Options())
	case http.MethodPost:
		var form manage.BackhaulForm
		if err := decodeJSON(w, r, &form); err != nil {
			http.Error(w, "invalid Backhaul form", http.StatusBadRequest)
			return
		}
		out, err := manage.CreateBackhaul(r.Context(), form)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, out)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
