package manage

import (
	"fmt"
	"strings"

	"github.com/backpack/backpack/internal/easytier"
	"github.com/backpack/backpack/internal/nativebackhaul"
	"github.com/backpack/backpack/internal/omni"
)

// SetupTuning is shared by the imported transports' setup forms. The preset
// supplies the default and a Fine Tune value overrides only the advertised knob.
type SetupTuning struct {
	Preset         string `json:"preset"`
	Value          *int   `json:"value"`
	RestartSeconds *int   `json:"restartSeconds"`
	MTU            *int   `json:"mtu"`
}

type SetupPerformance struct {
	Label         string         `json:"label"`
	Values        map[string]int `json:"values"`
	OverrideLabel string         `json:"overrideLabel"`
	Min           int            `json:"min"`
	Max           int            `json:"max"`
	Restart       bool           `json:"restart,omitempty"`
	MTU           bool           `json:"mtu,omitempty"`
}

func performance(label string, balance, turbo, aggressive, minValue, maxValue int) SetupPerformance {
	return SetupPerformance{Label: label, OverrideLabel: label, Min: minValue, Max: maxValue,
		Values: map[string]int{PresetBalance: balance, PresetTurbo: turbo, PresetAggressive: aggressive}}
}

var setupPerformance = buildSetupPerformance()

func buildSetupPerformance() map[string]SetupPerformance {
	m := map[string]SetupPerformance{}
	for _, mode := range nativebackhaul.Options() {
		p := performance("Parallel connections", 2, 4, 8, 1, 16)
		if mode.Value == "tun-tcp" {
			p = performance("Packet buffer (packets)", 32, 64, 256, 1, 1024)
			p.MTU = true
		}
		m["native-"+mode.Value] = p
	}
	for _, mode := range omni.Options() {
		p := performance("Interface transmit queue (packets)", 256, 1000, 4096, 32, 16384)
		p.Restart = mode["value"] != "fou" && mode["value"] != "vxlan"
		switch mode["value"] {
		case "mux", "ws":
			p = performance("Parallel connections", 8, 16, 32, 1, 64)
			p.Restart = true
		case "stripe":
			p = performance("Stripe window slots", 512, 768, 1024, 512, 2048)
			p.Restart = true
		case "hysteria":
			p = performance("Bandwidth target (Mbit/s)", 100, 200, 400, 1, 10000)
			p.Restart = true
		}
		m["omni-"+mode["value"]] = p
	}
	gost := performance("CPU scheduling weight", 50, 100, 200, 1, 10000)
	gost.Restart = true
	m["gost-direct"] = gost
	for _, carrier := range []string{"gretap", "l2tpv3", "easytier"} {
		p := performance("Interface transmit queue (packets)", 1024, 4096, 16384, 576, 1450)
		p.OverrideLabel = "MTU (bytes)"
		if carrier == "easytier" {
			p.OverrideLabel = "MTU before EasyTier encryption (bytes)"
			p.Min += easytier.EncryptedMTUOverhead
		}
		m[carrier] = p
	}
	return m
}

func SetupPerformanceOptions() map[string]SetupPerformance { return setupPerformance }

func setupTuningValue(id string, t SetupTuning) (int, error) {
	preset := strings.ToLower(strings.TrimSpace(t.Preset))
	options, ok := setupPerformance[id]
	if !ok {
		return 0, fmt.Errorf("unknown transport for performance tuning")
	}
	value := options.Values[preset]
	if preset != "" && value == 0 {
		return 0, fmt.Errorf("choose Balance, Turbo or Aggressive")
	}
	if t.Value != nil {
		if *t.Value < options.Min || *t.Value > options.Max {
			return 0, fmt.Errorf("%s must be between %d and %d", options.OverrideLabel, options.Min, options.Max)
		}
		value = *t.Value
	}
	if t.RestartSeconds != nil && (!options.Restart || *t.RestartSeconds < 1 || *t.RestartSeconds > 60) {
		return 0, fmt.Errorf("restart delay must be supported by the transport and between 1 and 60 seconds")
	}
	if t.MTU != nil && (!options.MTU || *t.MTU < 576 || *t.MTU > 1400) {
		return 0, fmt.Errorf("MTU must be supported by the transport and between 576 and 1400 bytes")
	}
	return value, nil
}
