package gostdirect

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
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
}
type Result struct {
	Token string `json:"token"`
	Unit  string `json:"unit"`
}

func Install(ctx context.Context, f Form) (Result, error) {
	var result Result
	if f.Action != "setup" && f.Action != "join" {
		return result, errors.New("choose setup or join")
	}
	body, err := json.Marshal(f)
	if err != nil {
		return result, err
	}
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
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
