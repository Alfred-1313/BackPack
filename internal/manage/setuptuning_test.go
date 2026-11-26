package manage

import (
	"reflect"
	"testing"

	"github.com/backpack/backpack/config"
)

func TestSetupPerformancePresets(t *testing.T) {
	for id, profile := range SetupPerformanceOptions() {
		t.Run(id, func(t *testing.T) {
			values := map[int]bool{}
			for _, preset := range []string{PresetBalance, PresetTurbo, PresetAggressive} {
				got, err := setupTuningValue(id, SetupTuning{Preset: preset})
				if err != nil || got != profile.Values[preset] || got <= 0 || values[got] {
					t.Fatalf("%s: got %d instead of a distinct positive value, error %v", preset, got, err)
				}
				values[got] = true
			}
			override := profile.Max
			if n, err := setupTuningValue(id, SetupTuning{Preset: PresetBalance, Value: &override}); n != override || err != nil {
				t.Fatalf("override: got %d, error %v", n, err)
			}
			override++
			if _, err := setupTuningValue(id, SetupTuning{Value: &override}); err == nil {
				t.Fatal("accepted out-of-range override")
			}
			delay := 8
			_, err := setupTuningValue(id, SetupTuning{RestartSeconds: &delay})
			if (err == nil) != profile.Restart {
				t.Fatalf("unexpected restart-delay validation: %v", err)
			}
		})
	}
}

func TestOmniSetupOptionsUseEachCarrierKnob(t *testing.T) {
	for mode, want := range map[string][3]int{
		"udp": {0, 0, 256}, "reverse-mux": {0, 0, 256},
		"fou": {0, 0, 256}, "vxlan": {0, 0, 256}, "icmp": {0, 0, 256},
		"tcp": {0, 0, 256}, "ackless": {0, 0, 256},
		"mux": {8, 0, 0}, "ws": {8, 0, 0}, "stripe": {512, 0, 0},
		"hysteria": {0, 100, 0},
	} {
		got, err := omniSetupOptions(mode, SetupTuning{Preset: PresetBalance})
		if err != nil || [3]int{got.Connections, got.BandwidthMbps, got.Queue} != want {
			t.Errorf("%s: got %+v, error %v", mode, got, err)
		}
	}
}

func TestNativePresetOverridesSurviveTheSetupLink(t *testing.T) {
	for _, settings := range []config.BackhaulConfig{
		{Mode: "xtcpmux", Lanes: 6},
		{Mode: "tun-tcp", PacketQueue: 72, MTU: 1300},
	} {
		settings.Native, settings.Preset, settings.Role = true, PresetAggressive, "server"
		settings.IranHost, settings.Port, settings.Token = "192.0.2.7", 7280, "fixture"
		want := backhaulShare("peer", settings)
		encoded, err := want.Encode()
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeShareLink(encoded)
		want.V = 1
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tuning did not round trip: %v", settings.Mode, err)
		}
	}
}
