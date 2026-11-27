package webui

import (
	"context"
	"net/http"
	"time"

	"github.com/backpack/backpack/internal/manage"
)

func (s *server) handleMatrix(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var out any
	var err error
	switch r.Method {
	case http.MethodGet:
		out, err = manage.ReadMatrixStatus(r.URL.Query().Get("name"))
	case http.MethodPost:
		var request struct {
			Action string
			Name   string
			Form   manage.MatrixForm
			Edit   manage.MatrixEdit
		}
		if decodeJSON(w, r, &request) != nil {
			http.Error(w, "invalid Matrix request", http.StatusBadRequest)
			return
		}
		switch request.Action {
		case "create":
			ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
			defer cancel()
			out, err = manage.StartMatrixSetup(ctx, s.ctx, request.Form)
		case "invite":
			out, err = manage.StartMatrixInvite(s.ctx, request.Name)
		case "edit":
			err = manage.EditMatrix(request.Edit)
			out = map[string]bool{"saved": err == nil}
		default:
			http.Error(w, "unknown Matrix action", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, out)
}
