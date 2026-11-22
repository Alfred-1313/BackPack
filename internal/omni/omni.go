// Package omni exposes pinned OmniTunnel transports through BackPack.
package omni

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/tui"
)

const revision = "acf4e6e5bacc4aa841320e39cff4b51e3f2a9eec"

var transports = []struct {
	value, title, description string
}{
	{"gre", "GRE (IPv4)", "unencrypted IPv4 peer-to-peer kernel GRE (protocol 47)"},
	{"icmp", "ICMP", "peer-to-peer IP over ICMP"},
	{"udp", "Routed UDP", "peer-to-peer encrypted IP over UDP"},
	{"tcp", "Routed TCP", "encrypted IP over a TCP stream"},
	{"mux", "Routed TCP Mux", "encrypted IP over parallel TCP streams"},
	{"ws", "Routed WebSocket", "encrypted IP over WebSocket"},
	{"hysteria", "Hysteria2", "QUIC with verified TLS and TCP/UDP forwards"},
	{"fou", "FoU", "peer-to-peer kernel GRE encapsulated in UDP"},
	{"vxlan", "VXLAN", "peer-to-peer kernel Ethernet overlay over UDP"},
	{"reverse-mux", "Reverse Mux", "Kharej dials Iran over the underlying ICMP tunnel"},
	{"stripe", "Stripe", "IP traffic striped across TCP connections"},
	{"ackless", "Ackless", "raw TCP packet carrier with authenticated frames"},
}

// Label is the shared display name used in setup and test results.
func Label(mode string) string {
	for _, transport := range transports {
		if transport.value == mode {
			return transport.title
		}
	}
	return strings.ToUpper(mode)
}

// Options exposes the same transport choices to the web panel.
func Options() []map[string]string {
	out := make([]map[string]string, 0, len(transports))
	for _, t := range transports {
		direction := "direct"
		if t.value == "reverse-mux" {
			direction = "reverse"
		}
		out = append(out, map[string]string{"value": t.value, "label": t.title, "desc": t.description, "direction": direction})
	}
	return out
}

const usage = `BackPack OmniTunnel transports

  backpack omni setup <transport> <name> <local-ipv4> <peer-ipv4> [peer-port]
  backpack omni join [transport]        paste a setup token on Kharej
  backpack omni list
  backpack omni status <name>
  backpack omni start|stop|remove <name>
  backpack omni forward <name> <tcp|udp> <port[:target]>
  backpack omni remove-forward <name> <port> [--force]
  backpack omni mux-forward <name> <port> <target-host> <target-port>
  backpack omni mux-remove <name> <port>
  backpack omni mux-join                paste a Reverse Mux forwarding token
  backpack omni mux-off <name>           remove the foreign-side mux forwarding

GRE uses IP protocol 47. The optional peer-port value becomes the GRE key.
`

var namePattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$")

type operation struct {
	args  []string
	token string
}

func parse(args []string, input io.Reader) (operation, error) {
	if len(args) == 0 {
		return operation{}, fmt.Errorf("choose an OmniTunnel command")
	}
	command := args[0]
	switch command {
	case "join", "mux-join":
		expected := ""
		if command == "join" && len(args) == 2 && knownTransport(args[1]) {
			expected = args[1]
		} else if len(args) != 1 {
			return operation{}, fmt.Errorf("paste the token on standard input")
		}
		raw, err := bufio.NewReader(io.LimitReader(input, 32<<10)).ReadString('\n')
		if err != nil && err != io.EOF {
			return operation{}, fmt.Errorf("read the setup token: %w", err)
		}
		token := strings.TrimSpace(raw)
		decoded, err := base64.StdEncoding.DecodeString(token)
		if err != nil || len(decoded) == 0 || len(raw) >= 32<<10 || strings.ContainsAny(token, "\r\n") {
			return operation{}, fmt.Errorf("invalid setup token")
		}
		if command == "mux-join" {
			return operation{args: []string{"mux-token"}, token: token}, nil
		}
		if expected != "" {
			fields := strings.SplitN(string(decoded), "|", 3)
			if len(fields) < 3 || fields[1] != expected {
				return operation{}, fmt.Errorf("the setup token does not match the selected %s transport", Label(expected))
			}
		}
		return operation{args: []string{"server-token"}, token: token}, nil
	case "setup":
		if (len(args) != 5 && len(args) != 6) || !knownTransport(args[1]) || !namePattern.MatchString(args[2]) {
			return operation{}, fmt.Errorf("usage: backpack omni setup <transport> <name> <local-ipv4> <peer-ipv4> [peer-port]")
		}
		local, peer := net.ParseIP(args[3]), net.ParseIP(args[4])
		if local.To4() == nil || peer.To4() == nil || local.IsUnspecified() || peer.IsUnspecified() || local.Equal(peer) {
			return operation{}, fmt.Errorf("enter two different IPv4 addresses assigned to the servers")
		}
		cmd := []string{"add-manual", args[1], args[2], peer.String(), "16", "none", local.String()}
		if len(args) == 6 {
			if !validPort(args[5]) {
				return operation{}, fmt.Errorf("peer port or GRE key must be 1-65535")
			}
			cmd = append(cmd, args[5])
		}
		return operation{args: cmd}, nil
	case "list", "test-ready":
		if len(args) == 1 {
			return operation{args: args}, nil
		}
	case "status", "start", "stop", "remove", "mux-off":
		if len(args) == 2 && namePattern.MatchString(args[1]) {
			if command == "start" {
				command = "enable"
			}
			return operation{args: []string{command, args[1]}}, nil
		}
	case "forward":
		if len(args) == 4 && namePattern.MatchString(args[1]) &&
			(args[2] == "tcp" || args[2] == "udp") && validPortMapping(args[3]) {
			return operation{args: append([]string{"pf-add"}, args[1:]...)}, nil
		}
	case "remove-forward":
		if (len(args) == 3 || (len(args) == 4 && args[3] == "--force")) &&
			namePattern.MatchString(args[1]) && validPort(args[2]) {
			return operation{args: append([]string{"pf-del"}, args[1:]...)}, nil
		}
	case "mux-remove":
		if len(args) == 3 && namePattern.MatchString(args[1]) && validPort(args[2]) {
			return operation{args: []string{"mux-del", args[1], args[2]}}, nil
		}
	case "mux-forward":
		if len(args) == 5 && namePattern.MatchString(args[1]) && validPort(args[2]) && validPort(args[4]) {
			return operation{args: append([]string{"mux-add"}, args[1:]...)}, nil
		}
	}
	return operation{}, fmt.Errorf("invalid OmniTunnel arguments. See backpack omni help")
}

func knownTransport(value string) bool {
	for _, transport := range transports {
		if transport.value == value {
			return true
		}
	}
	return false
}

func validPort(value string) bool {
	port, err := strconv.ParseUint(value, 10, 16)
	return err == nil && port > 0
}

func validPortMapping(value string) bool {
	port, target, hasTarget := strings.Cut(value, ":")
	return validPort(port) && (!hasTarget || validPort(target))
}

func helperPath() (string, error) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return "", fmt.Errorf("OmniTunnel requires Linux amd64 or arm64")
	}
	path := filepath.Join("/usr/local/lib/backpack/helpers/omnitunnel", revision+"-"+runtime.GOARCH, "manager")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return "", fmt.Errorf("install the OmniTunnel helper from the fork with BP_BUILD_FROM_SOURCE=1 BP_HELPERS=omni")
	}
	return path, nil
}

// Run is shared by the interactive menu and the automation entry point.
func Run(args []string, input io.Reader, stdout, stderr io.Writer) error {
	return RunContext(context.Background(), args, input, stdout, stderr)
}

func RunContext(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return nil
	}
	if f, ok := input.(*os.File); ok && (args[0] == "join" || args[0] == "mux-join") {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprint(stdout, "Setup Token: ")
		}
	}
	op, err := parse(args, input)
	if err != nil {
		return err
	}
	path, err := helperPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, op.args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	cmd.WaitDelay = 2 * time.Second
	if op.token != "" {
		cmd.Stdin = strings.NewReader(op.token + "\n")
	} else {
		cmd.Stdin = input
	}
	return cmd.Run()
}

// Menu keeps the original Setup, Manage and Connection Test numbering intact.
func Menu() {
	for {
		if _, err := helperPath(); err != nil {
			tui.Warn(err.Error())
			tui.PressEnter()
			return
		}
		choice := tui.ChooseOpt("OmniTunnel", []tui.Option{
			{Title: "Create Iran End"},
			{Title: "Apply Setup Token On Kharej"},
			{Title: "List Tunnels"},
			{Title: "Tunnel Status"},
			{Title: "Start Or Restart"},
			{Title: "Stop"},
			{Title: "Add TCP/UDP Forward"},
			{Title: "Remove Forward"},
			{Title: "Add Reverse Mux Forward"},
			{Title: "Apply Reverse Mux Token"},
			{Title: "Remove Reverse Mux Forward"},
			{Title: "Remove Local Tunnel"},
		})
		var args []string
		var input io.Reader = os.Stdin
		switch choice {
		case 0:
			opts := make([]tui.Option, len(transports))
			for i, transport := range transports {
				opts[i] = tui.Option{Title: transport.title, Desc: transport.description}
			}
			i := tui.ChooseOpt("OmniTunnel Transport", opts)
			if i < 0 {
				continue
			}
			mode := transports[i].value
			args = []string{"setup", mode, tui.PromptDefault("Tunnel Name", mode),
				tui.Prompt("Iran IPv4: "), tui.Prompt("Kharej IPv4: ")}
		case 1, 9:
			command := "join"
			if choice == 9 {
				command = "mux-join"
			}
			token, ok := tui.PromptOrEnd("Setup Token: ")
			if !ok || token == "" {
				continue
			}
			args, input = []string{command}, strings.NewReader(token)
		case 2:
			args = []string{"list"}
		case 3, 4, 5, 11:
			command := map[int]string{3: "status", 4: "start", 5: "stop", 11: "remove"}[choice]
			name := tui.Prompt("Tunnel Name: ")
			if choice == 11 && !tui.Confirm("Remove This Local Tunnel", false) {
				continue
			}
			args = []string{command, name}
		case 6:
			args = []string{"forward", tui.Prompt("Tunnel Name: "), tui.PromptDefault("Protocol", "tcp"),
				tui.Prompt("Public Port Or Public:Target: ")}
		case 7:
			name, port := tui.Prompt("Tunnel Name: "), tui.Prompt("Public Port: ")
			if !tui.Confirm("Remove Forward On Port "+port+" From "+name, false) {
				continue
			}
			args = []string{"remove-forward", name, port, "--force"}
		case 10:
			args = []string{"mux-remove", tui.Prompt("Tunnel Name: "), tui.Prompt("Public Port: ")}
		case 8:
			args = []string{"mux-forward", tui.Prompt("Tunnel Name: "), tui.Prompt("Public Port: "),
				tui.PromptDefault("Service Host On Kharej", "127.0.0.1"), tui.Prompt("Service Port: ")}
		default:
			return
		}
		if err := Run(args, input, os.Stdout, os.Stderr); err != nil {
			tui.Error(err.Error())
		}
		tui.PressEnter()
	}
}
