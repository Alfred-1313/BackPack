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

func setupExtraDirect(carrier string, side directSide) {
	defer tui.PressEnter()
	if carrier == "gost-direct" {
		form := gostdirect.Form{Action: "setup"}
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
	args := []string{"join"}
	var token string
	if side == sideKharej {
		token = tui.Prompt("OmniTunnel setup token: ")
	} else {
		mode := strings.TrimPrefix(carrier, "omni:")
		args = []string{"setup", mode, tui.Prompt("Tunnel name: "),
			tui.Prompt("Iran IPv4: "), tui.Prompt("Kharej IPv4: ")}
		if port := tui.Prompt("Kharej tunnel port (blank for automatic): "); port != "" {
			args = append(args, port)
		}
	}
	if err := omni.Run(args, strings.NewReader(token), os.Stdout, os.Stderr); err != nil {
		tui.Error(err.Error())
	}
}
