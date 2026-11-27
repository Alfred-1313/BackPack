package manage

import (
	"bytes"
	"fmt"
	"os"
	"slices"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/manage/tunnelspec"
)

// ApplyMTProxyForward changes one forwarding rule on the Iran endpoint. The
// full config is retained, including keys not represented by the panel.
func ApplyMTProxyForward(t Tunnel, rule string, remove bool) error {
	raw, err := os.ReadFile(app.ConfigPath(t.Name))
	if err != nil {
		return err
	}
	body, err := mtProxyForwardBody(raw, t, rule, remove)
	if err != nil || body == string(raw) {
		return err
	}
	if !core.IsActive(t.Service) {
		return app.WriteFileAtomic(app.ConfigPath(t.Name), []byte(body), app.TunnelConfigMode)
	}
	return tunnelspec.ApplyBody(t.Name, body, "MTProxy forwarding")
}

func mtProxyForwardBody(raw []byte, t Tunnel, rule string, remove bool) (string, error) {
	var doc map[string]any
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return "", err
	}
	var cfg config.Config
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return "", err
	}
	table, ports := "server", cfg.Server.Ports
	switch {
	case t.Role == "iran":
		table, ports = "l3", cfg.L3.Ports
	case t.Role == "server" && cfg.Backhaul.Native:
		table, ports = "backhaul", cfg.Backhaul.Ports
	case t.Role != "server":
		return "", fmt.Errorf("MTProxy forwards belong on the Iran endpoint")
	}
	settings, ok := doc[table].(map[string]any)
	if !ok {
		return "", fmt.Errorf("this tunnel has no editable forwarding table")
	}
	at := slices.Index(ports, rule)
	if (at >= 0) == !remove {
		return string(raw), nil
	}
	if remove {
		settings["ports"] = slices.Delete(ports, at, at+1)
	} else {
		settings["ports"] = append(ports, rule)
	}
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(doc); err != nil {
		return "", err
	}
	if patched, ok := preserveComments(string(raw), body.String()); ok {
		return patched, nil
	}
	return body.String(), nil
}
