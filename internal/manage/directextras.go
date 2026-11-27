package manage

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/omni"
	"github.com/backpack/backpack/internal/tui"
)

func setupExtraTransport(carrier string, side directSide) {
	defer tui.PressEnter()
	if version := gostdirect.ModeVersion(carrier); version != 0 {
		form := gostdirect.Form{Action: "setup", Version: version}
		if side == sideKharej {
			form.Action = "join"
			form.Token = tui.Prompt("GOST Direct setup token: ")
		} else {
			form.Name = tui.Prompt("Tunnel name: ")
			form.Peer = tui.Prompt("Kharej IPv4: ")
			form.Ingress = tui.Prompt("TCP port on Iran: ")
			form.Port = tui.Prompt("GOST TLS port on Kharej: ")
			form.Target = tui.Prompt("Loopback service port on Kharej: ")
		}
		result, err := gostdirect.Install(context.Background(), form)
		if err != nil {
			tui.Error(err.Error())
			return
		}
		fmt.Println(result.Unit)
		if result.Token != "" {
			fmt.Println("On Kharej run sudo backpack gost-direct join and paste this private token:")
			fmt.Println(result.Token)
		}
		return
	}
	mode := strings.TrimPrefix(carrier, "omni:")
	args := []string{"join", mode}
	var token string
	if side == sideKharej {
		token = tui.Prompt("OmniTunnel setup token: ")
	} else {
		args = []string{"setup", mode, tui.Prompt("Tunnel name: "),
			tui.Prompt("Iran IPv4: "), tui.Prompt("Kharej IPv4: ")}
		label := "Kharej tunnel port (blank for automatic): "
		if mode == "gre" {
			label = "GRE key (blank for automatic): "
		}
		if port := tui.Prompt(label); port != "" {
			args = append(args, port)
		}
	}
	if err := omni.Run(args, strings.NewReader(token), os.Stdout, os.Stderr); err != nil {
		tui.Error(err.Error())
	}
}
