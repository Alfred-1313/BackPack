package webui

import (
	"context"
	"net/http"

	"github.com/backpack/backpack/internal/transportprobe"
)

func (s *server) handleTransportProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var form struct {
		Address string `json:"address"`
		Code    string `json:"code"`
		Listen  bool   `json:"listen"`
	}
	if err := decodeJSON(w, r, &form); err != nil {
		http.Error(w, "invalid test form", http.StatusBadRequest)
		return
	}
	if form.Listen {
		code, err := transportprobe.Listen(context.Background(), form.Address)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"code": code})
		return
	}
	elapsed, err := transportprobe.Probe(r.Context(), form.Address, form.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]int64{"ms": elapsed.Milliseconds()})
}
