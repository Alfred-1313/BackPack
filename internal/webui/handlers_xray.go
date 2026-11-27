package webui

import (
	"context"
	"errors"
	"net/http"

	"github.com/backpack/backpack/internal/control"
	"github.com/backpack/backpack/internal/xraycore"
)

const jobXrayCore = "xray-core"

func writeXrayJob(w http.ResponseWriter, job control.Job) {
	writeJSON(w, map[string]any{
		"id": job.ID, "target": job.Target, "step": job.Step, "error": job.Err,
		"running": job.State == control.Running, "succeeded": job.State == control.Succeeded,
	})
}

func (s *server) handleXrayReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	releases, err := xraycore.Releases(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, releases)
}

func (s *server) handleXrayCore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" {
			job, ok := s.jobs.Latest(jobXrayCore)
			if ok && job.State == control.Running {
				writeXrayJob(w, job)
			} else {
				writeJSON(w, nil)
			}
			return
		}
		job, ok := s.jobs.Get(id)
		if !ok || job.Kind != jobXrayCore {
			http.Error(w, "Xray update status is no longer available. Check the installed version.", http.StatusNotFound)
			return
		}
		writeXrayJob(w, job)
	case http.MethodPost:
		var req struct {
			Version string `json:"version"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tag, err := xraycore.NormalizeTag(req.Version)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		job, err := s.jobs.Start(s.jobCtx(), jobXrayCore, tag, func(ctx context.Context, p *control.Progress) (any, error) {
			err := xraycore.Install(ctx, tag, func(step string) { p.Step("%s", step) })
			noteJob(err)
			return nil, err
		})
		var busy control.ErrBusy
		if errors.As(err, &busy) {
			http.Error(w, "An Xray update is already in progress for "+busy.Running.Target, http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeXrayJob(w, job)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
