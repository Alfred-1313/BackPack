package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/backpack/backpack/internal/control"
	"github.com/backpack/backpack/internal/waterwall"
)

const jobWaterWallCore = "waterwall-core"

func writeWaterWallJob(w http.ResponseWriter, job control.Job) {
	command := ""
	if job.State == control.Succeeded {
		command = waterwall.UpdateCommand(job.Target)
	}
	writeJSON(w, map[string]any{
		"id": job.ID, "target": job.Target, "step": job.Step, "error": job.Err,
		"running": job.State == control.Running, "succeeded": job.State == control.Succeeded,
		"command": command,
	})
}

func (s *server) handleWaterWallUpdateCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	check, err := waterwall.CheckUpdate(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	command := ""
	if check.Installed == check.Latest {
		command = waterwall.UpdateCommand(check.Installed)
	}
	writeJSON(w, struct {
		waterwall.UpdateCheck
		Command string `json:"command,omitempty"`
	}{check, command})
}

func (s *server) handleWaterWallCore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" {
			job, ok := s.jobs.Latest(jobWaterWallCore)
			if ok && job.State == control.Running {
				writeWaterWallJob(w, job)
			} else {
				writeJSON(w, nil)
			}
			return
		}
		job, ok := s.jobs.Get(id)
		if !ok || job.Kind != jobWaterWallCore {
			http.Error(w, "WaterWall update status expired. Check the installed version.", http.StatusNotFound)
			return
		}
		writeWaterWallJob(w, job)
	case http.MethodPost:
		var req struct {
			Version string `json:"version"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if waterwall.UpdateCommand(req.Version) == "" {
			http.Error(w, "invalid WaterWall release tag", http.StatusBadRequest)
			return
		}
		job, err := s.jobs.Start(s.jobCtx(), jobWaterWallCore, req.Version, func(ctx context.Context, p *control.Progress) (any, error) {
			err := updateWaterWallOnIran(ctx, req.Version, p)
			noteJob(err)
			return nil, err
		})
		var busy control.ErrBusy
		if errors.As(err, &busy) {
			http.Error(w, "WaterWall "+busy.Running.Target+" is already being installed", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeWaterWallJob(w, job)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func updateWaterWallOnIran(ctx context.Context, tag string, p *control.Progress) error {
	p.Step("Checking the official release…")
	check, err := waterwall.CheckUpdate(ctx)
	if err != nil {
		return err
	}
	if tag != check.Latest {
		return fmt.Errorf("the latest official WaterWall release is now %s. Check again before updating", check.Latest)
	}
	_, err = waterwall.InstallUpdate(ctx, tag, true, func(step string) { p.Step("%s", step) })
	return err
}
