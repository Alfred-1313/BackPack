package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/manage"
)

type rebuildRequest struct {
	Name     string
	Revision string
	Kind     string
	Form     json.RawMessage
}

type rebuildResponse struct {
	Active bool                 `json:"active"`
	Setup  manage.SetupLinkInfo `json:"setup"`
}

func (s *server) handleTunnelRebuild(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		info, err := manage.ReadRebuild(r.URL.Query().Get("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, info)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rebuildRequest
	var destination struct{ Name string }
	if decodeJSON(w, r, &req) != nil || json.Unmarshal(req.Form, &destination) != nil ||
		req.Name == "" || req.Name != strings.TrimSpace(destination.Name) {
		http.Error(w, "invalid replacement form", http.StatusBadRequest)
		return
	}
	plan, err := manage.PrepareRebuild(req.Name, req.Revision)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	out, err := s.rebuildDestination(r, req, plan)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, out)
}

func rebuildForm[F any](raw json.RawMessage, save func(F) (manage.SetupLinkInfo, error)) (manage.SetupLinkInfo, error) {
	var form F
	if err := json.Unmarshal(raw, &form); err != nil {
		return manage.SetupLinkInfo{}, errors.New("invalid settings for the selected transport")
	}
	return save(form)
}

func (s *server) rebuildDestination(r *http.Request, req rebuildRequest, plan manage.RebuildPlan) (rebuildResponse, error) {
	lifetime := plan.WithPeerJoin(s.ctx, req.Kind == "routed")
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	host := panelHost(r)
	commit := func(create func() (manage.SetupLinkInfo, error)) (manage.SetupLinkInfo, error) {
		var info manage.SetupLinkInfo
		err := plan.Apply(func() error {
			setup, err := create()
			if err == nil {
				info, err = plan.SetupCommand(setup)
			}
			return err
		})
		return info, err
	}

	var setup manage.SetupLinkInfo
	var err error
	switch req.Kind {
	case "reverse":
		setup, err = rebuildForm(req.Form, func(f manage.NewTunnel) (manage.SetupLinkInfo, error) {
			f.Role, f.Host, f.CheckControlPort = "server", host, true
			return commit(func() (manage.SetupLinkInfo, error) {
				if _, _, err := manage.CreateTunnel(f); err != nil {
					return manage.SetupLinkInfo{}, err
				}
				return manage.SetupLinkFor(req.Name, host)
			})
		})
	case "direct":
		setup, err = rebuildForm(req.Form, func(f manage.NewDirectTunnel) (manage.SetupLinkInfo, error) {
			f.Side = "iran"
			return commit(func() (manage.SetupLinkInfo, error) {
				if _, _, err := manage.CreateDirectTunnel(f); err != nil {
					return manage.SetupLinkInfo{}, err
				}
				return manage.SetupLinkFor(req.Name, host)
			})
		})
	case "direct-peer":
		setup, err = rebuildForm(req.Form, func(f manage.NewDirectTunnel) (manage.SetupLinkInfo, error) {
			return manage.StartDirectPeerSetup(lifetime, f)
		})
	case "routed":
		setup, err = rebuildForm(req.Form, func(f manage.RoutedPeerForm) (manage.SetupLinkInfo, error) {
			return manage.StartRoutedPeerSetup(lifetime, f)
		})
	case "matrix":
		setup, err = rebuildForm(req.Form, func(f manage.MatrixForm) (manage.SetupLinkInfo, error) {
			return manage.StartMatrixSetup(ctx, lifetime, f)
		})
	case "dagger":
		setup, err = rebuildForm(req.Form, func(f manage.DaggerForm) (manage.SetupLinkInfo, error) {
			create := func() (manage.SetupLinkInfo, error) {
				out, err := manage.CreateDaggerWithSetup(ctx, lifetime, f)
				return out.Setup, err
			}
			if f.Transport == "tun" && f.KharejIP == "" {
				return create()
			}
			return commit(create)
		})
	case "backhaul":
		setup, err = rebuildForm(req.Form, func(f manage.BackhaulForm) (manage.SetupLinkInfo, error) {
			if f.Mode == "tun-ipx" {
				out, err := manage.StartBackhaulIPX(ctx, lifetime, f)
				return out.Setup, err
			}
			return commit(func() (manage.SetupLinkInfo, error) {
				out, err := manage.CreateBackhaul(ctx, f)
				return out.Setup, err
			})
		})
	case "pengu":
		setup, err = rebuildForm(req.Form, func(f manage.PenguForm) (manage.SetupLinkInfo, error) {
			return commit(func() (manage.SetupLinkInfo, error) {
				out, err := manage.CreatePengu(ctx, f)
				return out.Setup, err
			})
		})
	default:
		err = errors.New("unknown replacement transport")
	}
	if err == nil && setup.Pending {
		setup, err = plan.SetupCommand(setup)
	}
	return rebuildResponse{Active: err == nil && !setup.Pending, Setup: setup}, err
}
