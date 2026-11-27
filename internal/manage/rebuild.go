package manage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/manage/tunnelspec"
)

const rebuildScheme = "backpack://r."

var rebuildMu sync.Mutex

// RebuildInfo supplies the common fields of the existing tunnel to Add Tunnel.
type RebuildInfo struct {
	Name      string   `json:"name"`
	Revision  string   `json:"revision"`
	Direction string   `json:"direction"`
	Transport string   `json:"transport"`
	Port      string   `json:"port,omitempty"`
	Ports     []string `json:"ports,omitempty"`
	Preset    string   `json:"preset,omitempty"`
}

func ReadRebuild(name string) (RebuildInfo, error) {
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	return readRebuild(name)
}

func readRebuild(name string) (RebuildInfo, error) {
	if err := core.CheckName(name); err != nil {
		return RebuildInfo{}, err
	}
	raw, err := os.ReadFile(app.ConfigPath(name))
	if err != nil {
		return RebuildInfo{}, fmt.Errorf("tunnel %q was not found", name)
	}
	cfg, err := core.LoadTunnelConfig(name)
	if err != nil {
		return RebuildInfo{}, err
	}
	for _, t := range core.List() {
		if t.Name != name {
			continue
		}
		id, side := peerRemovalIdentity(t, cfg)
		if side != "iran" || id == "" {
			return RebuildInfo{}, errors.New("rebuild this tunnel from its Iran end with a single identifiable peer")
		}
		info := RebuildInfo{Name: name, Revision: helperEditRevision(string(raw)), Direction: "reverse",
			Transport: strings.Replace(t.Transport, "/", "-", 1), Port: addrPort(t.Addr), Ports: t.Ports}
		switch {
		case cfg.Matrix.Present():
			info.Direction = cfg.Matrix.Direction
			info.Transport = "matrix-" + info.Direction
		case cfg.Pengu.Present():
			info.Direction, info.Preset = cfg.Pengu.Direction, cfg.Pengu.Preset
		case cfg.Backhaul.Present():
			info.Preset = cfg.Backhaul.Preset
			if cfg.Backhaul.Mode == "tun-ipx" {
				info.Direction, info.Port = "direct", ""
			}
		case cfg.Dagger.Present():
			if cfg.Dagger.Flavor == "tun" {
				info.Direction, info.Port = "direct", ""
			}
			if cfg.Dagger.Flavor == "quantum-gaming" {
				info.Transport = "dagger-quantum"
			}
		case cfg.L3.Enabled():
			info.Direction, info.Transport, info.Preset = "direct", cfg.L3.Carrier, cfg.L3.Preset
			if t.EasyTierReverse {
				info.Direction, info.Transport = "reverse", "easytier-reverse"
			}
		default:
			if settings, err := TunnelSettingsOf(name); err == nil {
				info.Port, info.Ports, info.Preset = settings.TunnelPort, settings.Ports, settings.Preset
				if settings.BindHost != "" && settings.BindHost != "0.0.0.0" && settings.BindHost != "::" {
					info.Port = t.Addr
				}
			}
		}
		return info, nil
	}
	return RebuildInfo{}, fmt.Errorf("tunnel %q was not found", name)
}

// RebuildPlan authorizes replacing exactly the configuration shown in Edit.
type RebuildPlan struct {
	name, revision, peer string
	external             bool
}

type rebuildContextKey struct{}

func PrepareRebuild(name, revision string) (RebuildPlan, error) {
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	info, err := readRebuild(name)
	if err != nil {
		return RebuildPlan{}, err
	}
	if revision != info.Revision {
		return RebuildPlan{}, errors.New("the tunnel changed after Edit opened. Reopen Edit to continue")
	}
	peer, err := RemovalLinkFor(Tunnel{Name: name, Role: "iran"})
	return RebuildPlan{name: name, revision: revision, peer: peer}, err
}

func (p RebuildPlan) WithPeerJoin(ctx context.Context, external bool) context.Context {
	p.external = external
	return context.WithValue(ctx, rebuildContextKey{}, p)
}

func canStageRebuild(ctx context.Context, name string) bool {
	p, ok := ctx.Value(rebuildContextKey{}).(RebuildPlan)
	return ok && p.name == name
}

func (p RebuildPlan) Apply(create func() error) error {
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	return replaceTunnel(p.name, p.revision, false, create)
}

// replaceTunnel holds the original config on disk until the new unit is up.
// The caller holds rebuildMu. Metadata such as counters, quota and schedules
// stays keyed to the original name.
func replaceTunnel(name, revision string, external bool, create func() error) error {
	path := app.ConfigPath(name)
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if helperEditRevision(string(original)) != revision {
		return errors.New("the tunnel changed before it could be rebuilt. Reopen Edit to continue")
	}
	service := app.ServiceName(name)
	unitPath := filepath.Join(app.ServiceDir, service)
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		return fmt.Errorf("could not read the existing tunnel unit: %w", err)
	}
	dir, err := os.MkdirTemp(app.ConfigDir, ".rebuild-")
	if err != nil {
		return err
	}
	backup := filepath.Join(dir, filepath.Base(path))
	defer os.Remove(dir)

	active, enabled := core.IsActive(service), core.IsEnabled(service)
	dial, edge, hosts := core.LinkDial(name), core.LinkEdge(name), core.LinkHosts(name)
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	rollback := func(cause error) error {
		stopErr := core.DisableService(service)
		if !core.IsActive(service) {
			stopErr = nil
		}
		configErr := os.Rename(backup, path)
		unitErr := os.WriteFile(unitPath, unit, 0644)
		core.SetLinkDial(name, dial, edge)
		core.SetLinkHosts(name, hosts)
		restoreErr := errors.Join(stopErr, configErr, unitErr, core.DaemonReload())
		if active && enabled {
			restoreErr = errors.Join(restoreErr, core.StartService(service))
		} else {
			if enabled {
				_, err := core.Systemctl("enable", service)
				restoreErr = errors.Join(restoreErr, err)
			}
			if active {
				_, err := core.Systemctl("start", service)
				restoreErr = errors.Join(restoreErr, err)
			}
		}
		if restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restoring %s: %w", name, restoreErr))
		}
		return cause
	}

	if err := core.DisableService(service); err != nil {
		return rollback(err)
	}
	if err := os.Remove(unitPath); err != nil {
		return rollback(err)
	}
	core.SetLinkDial(name, "", "")
	core.SetLinkHosts(name, nil)
	if err := create(); err != nil {
		return rollback(err)
	}
	if !external {
		if err := checkRebuiltUnit(service); err != nil {
			return rollback(err)
		}
	}
	tunnelspec.RecordChange(name, original, "rebuilt transport")
	_ = os.Remove(backup)
	return nil
}

func checkRebuiltUnit(unit string) error {
	pid, err := core.Systemctl("show", "-p", "MainPID", "--value", unit)
	if err != nil || pid == "" || pid == "0" {
		return fmt.Errorf("the replacement service %s did not start", unit)
	}
	time.Sleep(time.Second)
	current, err := core.Systemctl("show", "-p", "MainPID", "--value", unit)
	if err != nil || current != pid {
		return fmt.Errorf("the replacement service %s exited during startup", unit)
	}
	return nil
}

type rebuildLink struct {
	Peer  string `json:"p"`
	Setup string `json:"s"`
}

func (p RebuildPlan) SetupCommand(info SetupLinkInfo) (SetupLinkInfo, error) {
	raw := info.Link
	if raw == "" {
		raw = FindSetupLink(info.Apply)
	}
	if !strings.HasPrefix(raw, shareScheme) {
		return info, errors.New("could not generate a replacement setup link")
	}
	payload, err := json.Marshal(rebuildLink{Peer: p.peer, Setup: raw})
	if err != nil {
		return info, err
	}
	info.Link = rebuildScheme + base64.RawURLEncoding.EncodeToString(payload)
	info.Apply = "sudo backpack link apply '" + info.Link + "'"
	info.Install = ""
	info.Where = "Peer terminal"
	info.PanelDirectPeer, info.GRETAPPeer = nil, nil
	return info, nil
}

func decodeRebuildLink(raw string) (rebuildLink, error) {
	var link rebuildLink
	payload, ok := strings.CutPrefix(raw, rebuildScheme)
	if !ok || len(payload) > 65536 {
		return link, errors.New("invalid replacement setup link")
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(data, &link) != nil ||
		!strings.HasPrefix(link.Setup, shareScheme) || strings.HasPrefix(link.Setup, rebuildScheme) {
		return link, errors.New("invalid replacement setup link")
	}
	return link, nil
}

func applyRebuildLink(raw string, options LinkApplyOptions) (LinkApplied, error) {
	link, err := decodeRebuildLink(raw)
	if err != nil {
		return LinkApplied{}, err
	}
	peer, err := parseRemovalLink(link.Peer)
	if err != nil {
		return LinkApplied{}, err
	}
	rebuildMu.Lock()
	defer rebuildMu.Unlock()

	old, err := findRemovalPeer(peer, options.Name, core.List(), core.LoadTunnelConfig)
	var missing noLinkedPeerError
	if errors.As(err, &missing) {
		// An Iran tunnel may never have been installed on this Kharej. The
		// regular setup path still performs its own name and port checks.
		return ApplySetupLink(link.Setup, options)
	}
	if err != nil {
		return LinkApplied{}, err
	}
	options.Name = old.Name
	config, err := os.ReadFile(app.ConfigPath(old.Name))
	if err != nil {
		return LinkApplied{}, err
	}
	build := func() (LinkApplied, error) { return ApplySetupLink(link.Setup, options) }
	external := false
	if strings.HasPrefix(link.Setup, peerJoinScheme) {
		result, err := fetchPeerJoin(link.Setup)
		if err != nil {
			return LinkApplied{}, err
		}
		external = result.Driver == "gost" || result.Driver == "omni"
		build = func() (LinkApplied, error) { return applyPeerJoinResult(result, options) }
	}

	var out LinkApplied
	err = replaceTunnel(old.Name, helperEditRevision(string(config)), external, func() error {
		var err error
		out, err = build()
		if err == nil && !out.Active {
			err = errors.New("the replacement service did not start")
		}
		return err
	})
	out.Updated = err == nil
	return out, err
}
