package webui

import (
	"net/http"

	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/pengutunnel"
)

func (s *server) handlePengu(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, pengutunnel.Options())
	case http.MethodPost:
		var form manage.PenguForm
		if err := decodeJSON(w, r, &form); err != nil {
			http.Error(w, "invalid PenguTunnel form", http.StatusBadRequest)
			return
		}
		out, err := manage.CreatePengu(r.Context(), form)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, out)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
