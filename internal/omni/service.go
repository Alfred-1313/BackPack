package omni

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/backpack/backpack/internal/app"
)

type SetupOptions struct {
	Connections    int `json:"connections,omitempty"`
	BandwidthMbps  int `json:"bandwidthMbps,omitempty"`
	Queue          int `json:"queue,omitempty"`
	RestartSeconds int `json:"restartSeconds,omitempty"`
}

func ServiceName(name string) string { return "backpack-omni-" + name + ".service" }

func tuningPath(name string) string {
	return filepath.Join(app.ServiceDir, ServiceName(name)+".d", "backpack-tuning.conf")
}

var interfaceName = regexp.MustCompile(`^bpo[A-Za-z0-9_-]{1,12}$`)

// ConfigureService preserves settings when the pinned manager rewrites its
// unit. The live queue is applied after the manager has brought up the link,
// and an ExecStartPost applies it again on service restart.
func ConfigureService(ctx context.Context, name string, options SetupOptions) error {
	if options.Queue == 0 && options.RestartSeconds == 0 {
		return nil
	}
	if !namePattern.MatchString(name) || options.Queue < 0 || options.Queue > 16384 ||
		options.RestartSeconds < 0 || options.RestartSeconds > 60 {
		return errors.New("invalid OmniTunnel service tuning")
	}
	var body strings.Builder
	body.WriteString("[Service]\n")
	if options.RestartSeconds > 0 {
		fmt.Fprintf(&body, "RestartSec=%d\n", options.RestartSeconds)
	}
	var ip string
	var queueArgs []string
	if options.Queue > 0 {
		conf, err := os.ReadFile(filepath.Join(app.ConfigDir, "omnitunnel", "inst", name, "instance.conf"))
		if err != nil {
			return err
		}
		var dev string
		for _, line := range strings.Split(string(conf), "\n") {
			if v, ok := strings.CutPrefix(line, "DEV="); ok {
				dev = v
				break
			}
		}
		if !interfaceName.MatchString(dev) {
			return errors.New("OmniTunnel did not report its managed interface")
		}
		ip, err = exec.LookPath("ip")
		if err != nil {
			return err
		}
		queueArgs = []string{"link", "set", "dev", dev, "txqueuelen", strconv.Itoa(options.Queue)}
		fmt.Fprintf(&body, "ExecStartPost=-%s %s\n", ip, strings.Join(queueArgs, " "))
	}
	path := tuningPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := app.WriteFileAtomic(path, []byte(body.String()), 0644); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "systemctl", "daemon-reload").Run(); err != nil {
		return err
	}
	if ip != "" {
		out, err := exec.CommandContext(ctx, ip, queueArgs...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("apply the interface transmit queue: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

func removeServiceTuning(ctx context.Context, name string) error {
	path := tuningPath(name)
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(path))
	return exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
}
