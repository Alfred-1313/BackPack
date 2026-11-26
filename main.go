package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/backpack/backpack/cmd"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/cli"
	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/localproxy"
	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/menu"
	"github.com/backpack/backpack/internal/monitor"
	"github.com/backpack/backpack/internal/omni"
	"github.com/backpack/backpack/internal/telegram"
	"github.com/backpack/backpack/internal/transportprobe"
	"github.com/backpack/backpack/internal/utils"
	"github.com/backpack/backpack/internal/webui"
)

var logger = utils.NewLogger("info")

func setCLIOutput() {
	if st, err := os.Stdout.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		cli.Color = os.Getenv("NO_COLOR") == ""
		cli.Progress = func(s string) { fmt.Print(s) }
	}
}

func exitCLI(r cli.Result) {
	if r.Out != "" {
		fmt.Print(r.Out)
	}
	if r.Err != "" {
		fmt.Fprint(os.Stderr, r.Err)
	}
	os.Exit(r.Code)
}

// main has two modes:
//
//   - Engine mode:  `backpack -c /etc/backpack/<name>.toml`
//     Runs a single tunnel (server or client). This is what the systemd
//     units execute. Behaviour is identical to the original engine.
//
//   - Menu mode:    `backpack`  (no arguments)
//     Opens the interactive management CLI on the VPS.
func main() {
	if len(os.Args) == 3 && os.Args[2] == "join" && (os.Args[1] == "dagger" || os.Args[1] == "backhaul") {
		join, label := manage.JoinDagger, "DaggerConnect"
		if os.Args[1] == "backhaul" {
			join, label = manage.JoinBackhaul, "Backhaul"
		}
		if os.Geteuid() != 0 {
			fmt.Fprintf(os.Stderr, "run the %s join command with sudo\n", label)
			os.Exit(1)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		input := bufio.NewScanner(os.Stdin)
		input.Buffer(make([]byte, 4096), 16<<10)
		if !input.Scan() {
			cancel()
			fmt.Fprintf(os.Stderr, "read the %s setup token from standard input\n", label)
			os.Exit(1)
		}
		done, err := join(ctx, input.Text())
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		setCLIOutput()
		exitCLI(cli.ReportLink(done))
	}
	if len(os.Args) > 1 && os.Args[1] == "transport-test" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := transportprobe.Command(ctx, os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "gost-direct" && os.Args[2] == "join" {
		fmt.Fprintln(os.Stderr, "Paste the GOST Direct setup token:")
		input := bufio.NewScanner(os.Stdin)
		if !input.Scan() {
			os.Exit(1)
		}
		res, err := gostdirect.Install(context.Background(), gostdirect.Form{Action: "join", Token: input.Text()})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(res.Unit)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "omni" {
		if err := omni.Run(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Handled before the flags, because it is a subcommand with flags of its
	// own: `backpack node setup --panel ... --key ...`. The flag package would
	// stop at "node" and report the rest as unknown.
	if len(os.Args) > 1 && os.Args[1] == "node" {
		runNode(os.Args[2:])
		return
	}

	// The non-interactive commands, for the same reason and in the same place:
	// they are subcommands with arguments of their own, and the flag package
	// would stop at the first one and report the rest as unknown.
	//
	// Everything they do is in internal/cli, which returns what to print and
	// what to exit with rather than doing either — so the whole surface is
	// testable without a process. This is the only part that needs one.
	if len(os.Args) > 1 && cli.IsCommand(os.Args[1]) {
		cli.EngineCheck = cmd.CheckConfigFile
		setCLIOutput()
		exitCLI(cli.Run(os.Args[1:]))
	}

	configPath := flag.String("c", "", "path to a tunnel configuration file (TOML) — runs in engine mode")
	showVersion := flag.Bool("v", false, "print the version and exit")
	restartAll := flag.Bool("restart-all", false, "restart every configured tunnel and exit (used by the auto-refresh job)")
	tgReport := flag.Bool("telegram-report", false, "send a Telegram status report and exit (used by the scheduled job)")
	webPanel := flag.Bool("webui", false, "run the web panel (used by the backpack-webui service)")
	monitorMode := flag.Bool("monitor", false, "run the watchdog, Telegram bot and alerts (used by the backpack-monitor service)")
	proxyMode := flag.Bool("proxy", false, "run the built-in SOCKS5/HTTP proxy (used by the backpack-proxy service)")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(app.Version)
		fmt.Println(app.SourceURL)
		return
	case *restartAll:
		ok, failed := manage.RestartAll()
		fmt.Printf("restarted %d tunnels, %d failed\n", ok, failed)
		return
	case *tgReport:
		if err := telegram.SendStatusNow(); err != nil {
			logger.Errorf("telegram report failed: %v", err)
			os.Exit(1)
		}
		return
	case *webPanel:
		if err := webui.Serve(); err != nil {
			logger.Fatalf("web panel failed: %v", err)
		}
		return
	case *monitorMode:
		monitor.Run()
		return
	case *proxyMode:
		runProxy()
		return
	}

	// No config file -> interactive menu.
	if *configPath == "" {
		menu.Run()
		return
	}

	runEngine(*configPath)
}

// runProxy runs the built-in proxy until a termination signal arrives. The
// proxy is a plain loopback service; the tunnel forwards to it like any backend.
func runProxy() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go localproxy.Run(ctx)
	<-sigChan
	cancel()
	logger.Info("backpack proxy stopped")
}

// runEngine starts a single tunnel from a TOML config and blocks until a
// termination signal arrives, then shuts down gracefully.
func runEngine(configPath string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv(app.ConnTestParentPipe) == "1" {
		// The connection tester keeps the write end open for the lifetime of
		// this engine. EOF also arrives when the tester is killed abruptly.
		parentCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			var unused [1]byte
			_, _ = os.Stdin.Read(unused[:])
			cancel()
		}()
		ctx = parentCtx
	}
	cmd.Run(configPath, ctx)
	logger.Info("backpack engine stopped")
}
