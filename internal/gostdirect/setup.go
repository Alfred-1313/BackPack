package gostdirect

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

//go:embed setup.py
var script string

type Form struct {
	Action  string `json:"action"`
	Name    string `json:"name"`
	Peer    string `json:"peer"`
	Port    string `json:"port"`
	Target  string `json:"target"`
	Ingress string `json:"ingress"`
	Token   string `json:"token"`
	Secret  string `json:"secret,omitempty"`
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
