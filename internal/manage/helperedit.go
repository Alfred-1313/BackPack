package manage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/daggerconnect"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/manage/tunnelspec"
	"github.com/backpack/backpack/internal/matrix"
	"github.com/backpack/backpack/internal/nativebackhaul"
	"github.com/backpack/backpack/internal/pengutunnel"
)

type HelperEditField struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Tab     string   `json:"tab"`
	Type    string   `json:"type"`
	Value   any      `json:"value"`
	Choices []string `json:"choices,omitempty"`
	Min     int      `json:"min"`
	Max     int      `json:"max,omitempty"`
	shared  bool
}

type HelperSettings struct {
	Family    string            `json:"family"`
	Transport string            `json:"transport"`
	Revision  string            `json:"revision"`
	Fields    []HelperEditField `json:"fields"`
}

type HelperEdit struct {
	Revision string                     `json:"revision"`
	Changes  map[string]json.RawMessage `json:"changes"`
}

func helperEditRevision(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func HelperSettingsOf(name string) (HelperSettings, bool, error) {
	if err := core.CheckName(name); err != nil {
		return HelperSettings{}, false, err
	}
	raw, err := os.ReadFile(app.ConfigPath(name))
	if err != nil {
		return HelperSettings{}, false, err
	}
	var cfg config.Config
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return HelperSettings{}, false, err
	}
	_, settings := helperSettings(cfg)
	settings.Revision = helperEditRevision(string(raw))
	return settings, settings.Family != "", nil
}

func helperSettings(cfg config.Config) (string, HelperSettings) {
	var s HelperSettings
	const connection, ports, tune, carrier = "Connection", "Ports", "Fine-tune", "Carrier"
	add := func(key, label, tab, kind string, value any, shared bool, min, max int, choices ...string) {
		s.Fields = append(s.Fields, HelperEditField{key, label, tab, kind, value, choices, min, max, shared})
	}
	input := func(key, label, tab, value string, shared bool) {
		add(key, label, tab, "text", value, shared, 0, 0)
	}
	number := func(key, label, tab string, value, min, max int, shared bool) {
		add(key, label, tab, "number", value, shared, min, max)
	}
	choice := func(key, label, tab, value string, shared bool, options ...string) {
		add(key, label, tab, "select", value, shared, 0, 0, options...)
	}
	forwards := func(value string) { add("ports", "Forwarded ports", ports, "ports", value, false, 0, 0) }
	preset := func(value string) {
		choice("preset", "Performance preset", connection, value, true, "custom", "balance", "turbo", "aggressive")
	}

	switch {
	case cfg.Matrix.Present():
		c := cfg.Matrix
		s.Family, s.Transport = "Matrix", "Tinc"
		forwards(strings.Join(c.Ports, ", "))
		add("accept_udp", "Forward UDP as well as TCP", ports, "checkbox", c.AcceptUDP, false, 0, 0)
		number("mtu", "MTU (bytes)", tune, c.MTU, 0, 1500, false)
		number("txqueuelen", "Transmit queue length", tune, c.TxQueueLen, 0, 65536, false)
		return "matrix", s

	case cfg.Pengu.Present():
		c := cfg.Pengu
		mode, _ := pengutunnel.Find(c.Mode)
		s.Family, s.Transport = "PenguTunnel", orDefault(mode.Label, c.Mode)
		if slices.Contains([]string{"num1", "num2", "num3"}, c.Mode) {
			choice("mode", "Transport", connection, c.Mode, true, "num1", "num2", "num3")
		}
		input("iran_host", "Iran address", connection, c.IranHost, true)
		if c.Direction == "direct" || mode.IPX {
			input("kharej_host", "Kharej address", connection, c.KharejHost, true)
		}
		if !mode.IPX {
			number("port", "Tunnel port", connection, int(c.Port), 1, 65535, true)
			number("pool", "Parallel connections", tune, c.Pool, 1, 32, true)
		}
		number("health_port", "Health check port", tune, int(c.HealthPort), 1, 65535, true)
		choice("preset", "Performance preset", connection, c.Preset, true, "balance", "turbo", "aggressive")
		if c.Side == "iran" {
			forwards(strings.Join(c.Ports, ", "))
		}
		number("workers", "Worker CPU limit", tune, c.Workers, 0, 64, true)
		if mode.TUN {
			number("mtu", "MTU (bytes)", tune, c.MTU, 576, 1500, true)
		} else {
			number("window", "Stream window (MB)", tune, c.Window, 0, 256, true)
		}
		choice("cipher", "Encryption", carrier, c.Cipher, true, "aes-256-gcm", "chacha20-poly1305")
		if c.Mode == "tcpfwd" {
			choice("header", "TCP carrier header", carrier, c.Header, true, "", "PGprotocol-NUM1", "PGprotocol-NUM2", "PGprotocol-NUM3")
		}
		if c.Mode == "tls" || c.Mode == "xhttps" {
			input("tls_name", "TLS server name", carrier, c.TLSName, true)
		}
		if c.Mode == "xhttp" || c.Mode == "xhttps" {
			input("http_path", "HTTP POST path", carrier, c.HTTPPath, true)
		}
		return "pengu", s

	case cfg.Backhaul.Present():
		c := cfg.Backhaul
		option, _ := backhaulpremium.Find(c.Mode)
		s.Family = "Backhaul"
		if c.Native {
			option, _ = nativebackhaul.Find(c.Mode)
			s.Family = "BackPack"
		}
		s.Transport = orDefault(option.Label, c.Mode)
		if strings.HasPrefix(c.Mode, "dagger-") {
			s.Transport = strings.ToUpper(c.Mode)
		}
		switch c.Mode {
		case "xtcpmux", "xwsmux":
			choice("mode", "Transport", connection, c.Mode, true, "xtcpmux", "xwsmux")
		case "dagger-http", "dagger-https":
			if c.Native {
				choice("mode", "Transport", connection, c.Mode, true, "dagger-http", "dagger-https")
			}
		}
		input("iran_host", "Iran address", connection, c.IranHost, true)
		if c.Mode != "tun-ipx" {
			number("port", "Tunnel port", connection, int(c.Port), 1, 65535, true)
		} else {
			input("kharej_ip", "Kharej IPv4", connection, c.KharejIP, true)
			input("ipx_profile", "IPX profile", carrier, c.IPXProfile, true)
		}
		if c.Role == "server" {
			forwards(strings.Join(c.Ports, ", "))
		}
		if c.Native {
			preset(orDefault(c.Preset, "custom"))
		}
		if backhaulpremium.IsTUN(c.Mode) {
			number("mtu", "MTU (bytes)", tune, c.MTU, 0, 1500, true)
			if c.Native {
				number("packet_queue", "Packet buffer (packets)", tune, c.PacketQueue, 0, 1024, true)
			} else {
				number("health_port", "Health check port", tune, int(c.HealthPort), 1, 65535, true)
			}
		} else if c.Native {
			number("lanes", "Parallel connections", tune, c.Lanes, 0, 16, true)
		}
		if strings.HasPrefix(c.Mode, "dagger-") {
			input("http_path", "HTTP path", carrier, c.HTTPPath, true)
			if c.Mode != "dagger-xhttp" {
				input("http_host", "HTTP hostname", carrier, c.HTTPHost, true)
			}
		}
		if nativebackhaul.HasMuxOptions(c.Mode) {
			m := c.Mux
			if m == nil {
				m = &config.BackhaulMuxOptions{}
			}
			number("mux.heartbeat_seconds", "Heartbeat interval (seconds)", tune, m.HeartbeatSeconds, 0, 3600, true)
			number("mux.timeout_seconds", "Heartbeat timeout (seconds)", tune, m.TimeoutSeconds, 0, 3600, true)
			if c.Role == "server" {
				number("mux.max_connections", "Maximum connections", tune, m.MaxConnections, 0, 65535, false)
				number("mux.bandwidth_mbps", "Bandwidth limit (Mbps)", tune, m.BandwidthMbps, 0, 100000, false)
				add("mux.proxy_protocol", "PROXY protocol", ports, "checkbox", m.ProxyProtocol, false, 0, 0)
			}
		}
		return "backhaul", s

	case cfg.Dagger.Present():
		c := cfg.Dagger
		s.Family, s.Transport = "Dagger", c.Flavor
		input("iran_ip", "Iran IPv4", connection, c.IranIP, true)
		number("port", "Tunnel port", connection, int(c.Port), 1, 65535, true)
		if c.Role == "server" {
			forwards(c.Ports)
		}
		if c.Flavor == "tun" {
			input("kharej_ip", "Kharej IPv4", connection, c.KharejIP, true)
			number("heartbeat_seconds", "Heartbeat interval (seconds)", tune, c.HeartbeatSeconds, 0, 60, true)
		} else {
			number("connection_pool", "Parallel connections", tune, c.ConnectionPool, 0, 64, true)
		}
		if c.Flavor == "dc6" {
			input("iran_ipv6", "Iran IPv6", connection, c.IranIPv6, true)
		}
		switch c.Flavor {
		case "http", "https", "xhttp", "xhttps":
			input("http_path", "HTTP path", carrier, c.HTTPPath, true)
			switch c.Flavor {
			case "http", "https":
				input("http_host", "HTTP hostname", carrier, c.HTTPHost, true)
			default:
				choice("upload_mode", "Upload mode", carrier, orDefault(c.UploadMode, "auto"), true, "auto", "stream-up", "packet-up")
			}
			if c.Role == "server" && daggerconnect.UsesTLSFiles(c.Flavor) {
				input("cert_file", "TLS certificate file", carrier, c.CertFile, false)
				input("key_file", "TLS private key file", carrier, c.KeyFile, false)
			}
		case "quantum", "quantum-gaming":
			number("quantum_mtu", "Quantum MTU (bytes)", tune, c.QuantumMTU, 0, 9000, true)
			choice("quantum_cipher", "Quantum header cipher", carrier, orDefault(c.QuantumCipher, "aes"), true, "aes", "salsa20", "none")
		}
		return "dagger", s
	}
	return "", s
}

func helperEditValue(field HelperEditField, raw json.RawMessage) (any, error) {
	switch field.Type {
	case "number":
		var v int
		if err := json.Unmarshal(raw, &v); err == nil && v >= field.Min && v <= field.Max {
			return v, nil
		}
		return nil, fmt.Errorf("%s must be between %d and %d", field.Label, field.Min, field.Max)
	case "checkbox":
		var v bool
		if err := json.Unmarshal(raw, &v); err == nil {
			return v, nil
		}
	default:
		var v string
		if err := json.Unmarshal(raw, &v); err == nil && !strings.ContainsRune(v, '\x00') {
			v = strings.TrimSpace(v)
			if field.Type != "select" || slices.Contains(field.Choices, v) {
				return v, nil
			}
		}
	}
	return nil, fmt.Errorf("invalid value for %s", field.Label)
}

func prepareHelperEdit(name, raw string, changes map[string]json.RawMessage, peer bool) (string, config.Config, config.Config, map[string]json.RawMessage, error) {
	var before, after config.Config
	if _, err := toml.Decode(raw, &before); err != nil {
		return "", before, after, nil, err
	}
	table, form := helperSettings(before)
	if table == "" || len(changes) == 0 || len(changes) > len(form.Fields) {
		return "", before, after, nil, errors.New("no supported changes for this tunnel")
	}
	var doc map[string]any
	if _, err := toml.Decode(raw, &doc); err != nil {
		return "", before, after, nil, err
	}
	settings, ok := doc[table].(map[string]any)
	if !ok {
		return "", before, after, nil, errors.New("the tunnel settings are missing")
	}
	shared := make(map[string]json.RawMessage)
	for key, rawValue := range changes {
		at := slices.IndexFunc(form.Fields, func(f HelperEditField) bool { return f.Key == key })
		if at < 0 || peer && !form.Fields[at].shared || bytes.Equal(rawValue, []byte("null")) {
			return "", before, after, nil, fmt.Errorf("%q cannot be changed for this transport", key)
		}
		field := form.Fields[at]
		v, err := helperEditValue(field, rawValue)
		if err != nil {
			return "", before, after, nil, err
		}
		if key == "ports" {
			if table != "dagger" {
				rules := parsePorts(strings.ReplaceAll(v.(string), "\n", ","))
				if table == "pengu" {
					for i, rule := range rules {
						if port, ok := strings.CutPrefix(rule, "udp:"); ok {
							rules[i] = "udp/" + port
						} else {
							rules[i] = strings.TrimPrefix(rule, "tcp:")
						}
					}
				}
				v = rules
			}
		}
		target := settings
		if group, fieldKey, nested := strings.Cut(key, "."); nested {
			target, _ = settings[group].(map[string]any)
			if target == nil {
				target = make(map[string]any)
				settings[group] = target
			}
			target[fieldKey] = v
		} else {
			target[key] = v
		}
		if field.shared {
			shared[key], _ = json.Marshal(v)
		}
	}
	if rawPreset, changed := changes["preset"]; changed {
		var preset string
		_ = json.Unmarshal(rawPreset, &preset)
		applyHelperPreset(table, settings, preset, changes)
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(doc); err != nil {
		return "", before, after, nil, err
	}
	body := encoded.String()
	if patched, ok := preserveComments(raw, body); ok {
		body = patched
	}
	if _, err := toml.Decode(body, &after); err != nil {
		return "", before, after, nil, err
	}
	return body, before, after, shared, validateHelperEdit(name, after)
}

func applyHelperPreset(table string, settings map[string]any, preset string, changes map[string]json.RawMessage) {
	if preset == "custom" {
		if table == "backhaul" {
			delete(settings, "preset")
		}
		return
	}
	mode, _ := settings["mode"].(string)
	key := "lanes"
	if table == "pengu" {
		key = "pool"
		mode = "pengu-" + mode
		if choice, _ := pengutunnel.Find(strings.TrimPrefix(mode, "pengu-")); choice.IPX {
			key = "mtu"
		}
	} else {
		if mode == "tun-tcp" {
			key = "packet_queue"
		}
		mode = "native-" + mode
	}
	if _, manual := changes[key]; !manual {
		if value, err := setupTuningValue(mode, SetupTuning{Preset: preset}); err == nil && value != 0 {
			settings[key] = value
		}
	}
}

func validateHelperEdit(name string, c config.Config) error {
	switch {
	case c.Matrix.Present():
		return matrix.Validate(c.Matrix)
	case c.Pengu.Present():
		return pengutunnel.Validate(c.Pengu)
	case c.Backhaul.Present():
		if c.Backhaul.Native {
			return nativebackhaul.Validate(c.Backhaul)
		}
		return backhaulpremium.Validate(c.Backhaul)
	case c.Dagger.Present():
		return validateDaggerTunnel(name, c.Dagger)
	default:
		return errors.New("unsupported tunnel engine")
	}
}

func writeHelperEdit(name, raw, body string) error {
	if raw == body {
		return nil
	}
	if !core.IsActive(app.ServiceName(name)) {
		if err := app.WriteFileAtomic(app.ConfigPath(name), []byte(body), app.TunnelConfigMode); err != nil {
			return err
		}
		tunnelspec.RecordChange(name, []byte(raw), "panel edit")
		return nil
	}
	return tunnelspec.ApplyBodyChecked(name, body, "panel edit", func() error {
		service := app.ServiceName(name)
		state := func() (string, error) {
			out, err := core.Systemctl("show", "-p", "ActiveState", "-p", "MainPID", service)
			if err != nil || !strings.Contains("\n"+out+"\n", "\nActiveState=active\n") || strings.Contains("\n"+out+"\n", "\nMainPID=0\n") {
				return "", errors.New("the tunnel did not stay active")
			}
			return out, nil
		}
		before, err := state()
		if err != nil {
			return err
		}
		time.Sleep(1500 * time.Millisecond)
		if after, err := state(); err != nil || before != after {
			return errors.New("the tunnel did not stay active after restart")
		}
		return nil
	})
}
