package gostdirect

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

//go:embed setup.py
var script string

const ModeV3 = "gost-direct"
const ModeV2 = "gost-direct-v2"
const ModeV1 = "gost-direct-v1"

func ModeVersion(mode string) int {
	switch mode {
	case ModeV3:
		return 3
	case ModeV2:
		return 2
	case ModeV1:
		return 1
	default:
		return 0
	}
}

type TokenInfo struct {
	Name    string
	Target  int
	Version int
}

func ReadTokenInfo(raw string) (TokenInfo, error) {
	info := TokenInfo{}
	prefix, payload, ok := strings.Cut(strings.TrimSpace(raw), ".")
	if !ok || len(payload) > 24000 || (prefix != "bpgost1" && prefix != "bpgost2") {
		return info, errors.New("invalid GOST Direct setup token")
	}
	if prefix == "bpgost1" {
		info.Version = 3
	}
	body, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(body, &info) != nil ||
		info.Name == "" || info.Target < 1 || info.Target > 65535 ||
		(prefix == "bpgost1" && info.Version != 3) ||
		(prefix == "bpgost2" && info.Version != 1 && info.Version != 2) {
		return TokenInfo{}, errors.New("invalid GOST Direct setup token")
	}
	return info, nil
}

type Form struct {
	Action         string `json:"action"`
	Name           string `json:"name"`
	Peer           string `json:"peer"`
	Port           string `json:"port"`
	Target         string `json:"target"`
	Ingress        string `json:"ingress"`
	Token          string `json:"token"`
	Secret         string `json:"secret,omitempty"`
	Version        int    `json:"version,omitempty"`
	CPUWeight      int    `json:"cpuWeight,omitempty"`
	RestartSeconds int    `json:"restartSeconds,omitempty"`
}
type Result struct {
	Token string `json:"token"`
	Unit  string `json:"unit"`
}

func Install(ctx context.Context, f Form) (Result, error) {
	return install(ctx, f, false)
}

// RunTest owns a transient listener. The caller removes it after measurement.
func RunTest(ctx context.Context, f Form) (Result, error) {
	return install(ctx, f, true)
}

func install(ctx context.Context, f Form, test bool) (Result, error) {
	var result Result
	if f.Action != "setup" && f.Action != "join" && !(test && f.Action == "remove") {
		return result, errors.New("choose setup or join")
	}
	body, err := json.Marshal(struct {
		Form
		Test bool `json:"_test,omitempty"`
	}{f, test})
	if err != nil {
		return result, err
	}
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return result, errors.New(msg)
		}
		return result, err
	}
	err = json.Unmarshal(output, &result)
	return result, err
}
