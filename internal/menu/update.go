// Updates: the release channel, an update from the network or from a file,
// the relay used when the release host is unreachable, and restore points.

package menu

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage"
	"github.com/backpack/backpack/internal/telegram"
	"github.com/backpack/backpack/internal/tui"
)

func updateToolsMenu() {
	for {
		tui.Clear()
		idx := tui.ChooseOpt("Update Tools", []tui.Option{
			{Title: "Install From A File", Desc: localUpdateDesc()},
			{Title: "Restore Points", Desc: "go back to a previous version"},
			{Title: "Release Channel", Desc: "stable, or also pre-releases"},
		})
		switch idx {
		case 0:
			runLocalUpdate()
		case 1:
			restorePointMenu()
		case 2:
			channelMenu()
		default:
			return
		}
	}
}

// localUpdateDesc says whether there is a file to install, on the menu line, so
// the answer is visible before the option is chosen.
func localUpdateDesc() string {
	if u, ok := manage.FindLocalUpdate(); ok {
		if u.Version != "" {
			return "found " + u.Version + " in " + filepath.Dir(u.Path)
		}
		return "found " + filepath.Base(u.Path) + " in " + filepath.Dir(u.Path)
	}
	return "put " + manage.LocalAssetName() + " in /root first"
}

// runLocalUpdate installs a release the operator downloaded themselves.
//
// This exists because the download is the step that fails on the networks this
// project is for. Everything after it is the ordinary update — the same
// snapshot, health check and automatic rollback — so what is different here is
// only where the file came from.
func runLocalUpdate() {
	tui.Clear()
	tui.Title("Install From A File")
	fmt.Println()

	u, ok := manage.FindLocalUpdate()
	if !ok {
		tui.Error("No " + manage.LocalAssetName() + " found.")
		fmt.Println()
		tui.Info("Download It From GitHub Releases, Copy It Here, And Try Again:")
		fmt.Println()
		fmt.Printf("  %sscp %s root@this-server:/root/%s\n\n", tui.Gray, manage.LocalAssetName(), tui.Reset)
		tui.Info("Looked in: " + strings.Join(manage.LocalUpdateSearchedIn(), ", "))
		tui.Info("Exact Name Required — This Server Is " + runtime.GOARCH + ".")
		tui.PressEnter()
		return
	}

	fmt.Printf("  %sFile%s     %s\n", tui.Gray, tui.Reset, u.Path)
	fmt.Printf("  %sSize%s     %.1f MB\n", tui.Gray, tui.Reset, float64(u.Size)/(1<<20))
	fmt.Printf("  %sAdded%s    %s\n", tui.Gray, tui.Reset, u.When.Format("2006-01-02 15:04"))
	if u.Version != "" {
		fmt.Printf("  %sVersion%s  %s%s%s  (this server runs %s)\n",
			tui.Gray, tui.Reset, tui.Bold+tui.White, u.Version, tui.Reset, app.Version)
	} else {
		fmt.Printf("  %sVersion%s  %sunknown — the binary inside did not answer%s\n",
			tui.Gray, tui.Reset, tui.Gray, tui.Reset)
	}
	if u.Checksums != "" {
		fmt.Printf("  %sChecksum%s %s\n", tui.Gray, tui.Reset, u.Checksums)
	} else {
		fmt.Printf("  %sChecksum%s %sno SHA256SUMS beside it — it will be installed unverified%s\n",
			tui.Gray, tui.Reset, tui.Gray, tui.Reset)
	}
	fmt.Println()

	// Said plainly rather than refused. Reinstalling the same version is a
	// reasonable thing to want — a binary that was corrupted, a rollback being
	// undone — and going backwards is sometimes the whole point.
	if u.Version != "" && u.Version == app.Version {
		tui.Warn("Same Version As Running — Reinstalling Is Fine.")
	}

	tui.Info("A Restore Point Is Taken First; A Failed Update Rolls Back.")
	fmt.Println()
	if !tui.Confirm("Install It", true) {
		return
	}

	fmt.Println()
	if err := manage.ApplyLocalUpdate(u, func(l string) { tui.Info("• " + l) }); err != nil {
		tui.Error(err.Error())
		tui.PressEnter()
		return
	}
	tui.Success("Done.")
	tui.PressEnter()
	reopen()
}

// channelMenu picks between stable releases and pre-releases.
func channelMenu() {
	tui.Clear()
	tui.Title("Release Channel")
	fmt.Println()
	tui.Info("Now: " + manage.ChannelLabel())
	fmt.Println()
	tui.Warn("Beta Also Installs Pre-Releases — Riskier For A Busy Server.")
	fmt.Println()

	opts, values := manage.ChannelOptions()
	idx := tui.ChooseOpt("Channel", opts)
	if idx < 0 {
		return
	}
	if err := manage.SetChannel(values[idx]); err != nil {
		tui.Error("Could not save the channel: " + err.Error())
		tui.PressEnter()
		return
	}
	tui.Success("Channel: " + manage.ChannelLabel() + ".")
	tui.PressEnter()
}

// runUpdate applies the latest release in one step and reopens the menu.
func runUpdate(apply func(func(string)) (bool, error)) {
	tui.Clear()
	tui.Title("Update Backpack")
	tui.Info("Checking the fork for updates...")
	updated, err := apply(func(line string) { tui.Info("• " + line) })
	if err != nil {
		tui.Error("Update failed: " + err.Error())
		return
	}
	if updated {
		tui.Success("Updated.")
		reopen()
	}
}

// restorePointMenu lists saved restore points and can roll back to one.
func restorePointMenu() {
	tui.Clear()
	tui.Title("Restore Points")
	tui.Warn("Taken Before Every Update (Binary And Configs).")
	fmt.Println()

	points := manage.ListSnapshots()
	if len(points) == 0 {
		tui.Info("None Yet.")
		tui.PressEnter()
		return
	}

	opts := make([]tui.Option, len(points))
	for i, p := range points {
		desc := fmt.Sprintf("version %s", p.Meta.Version)
		if n := len(p.Meta.Tunnels); n > 0 {
			desc += fmt.Sprintf(" · %d tunnel(s)", n)
		}
		opts[i] = tui.Option{Title: p.Meta.Stamp, Desc: desc}
	}
	idx := tui.ChooseOpt("Roll Back To", opts)
	if idx < 0 {
		return
	}

	chosen := points[idx]
	fmt.Println()
	tui.Warn("Restores The Binary And All Configs From " + chosen.Meta.Stamp + ", Then Restarts Everything.")
	if !tui.Confirm("Roll Back", false) {
		return
	}
	fmt.Println()
	if err := manage.RollbackUpdate(chosen, func(l string) { tui.Info("• " + l) }); err != nil {
		tui.Error("Rollback failed: " + err.Error())
		tui.PressEnter()
		return
	}
	tui.Success("Rolled Back To " + chosen.Meta.Version + ".")
	tui.PressEnter()
	reopen()
}

// reopen replaces this menu with the binary now installed.
//
// An update or a rollback swaps the file on disk, and this process is still
// the build it was started as: the menu came back showing the old version and
// the old screens until the operator quit and ran sudo backpack again. Every
// tunnel has already been restarted on the new binary by then; this puts the
// menu on it too.
func reopen() {
	tui.Info("Opening The New Version...")
	if err := execSelf(); err != nil {
		tui.Warn("Could Not Reopen (" + err.Error() + ") — Run sudo backpack Again.")
	}
}

// execSelf starts the installed binary in place of this process. A variable so
// a test can see it asked for without the test binary being replaced.
var execSelf = func() error {
	return syscall.Exec(app.BinPath, []string{app.BinPath}, os.Environ())
}

// diagnoseRelay walks the relay chain and reports the first broken hop.
func diagnoseRelay() {
	tui.Clear()
	tui.Title("Relay Diagnosis")
	fmt.Println()
	tui.Warn("Checking Each Hop To Telegram...")
	fmt.Println()

	steps := telegram.DiagnoseRelay()
	for _, s := range steps {
		mark := tui.Color(tui.Bold+tui.Red, "✗")
		if s.OK {
			mark = tui.Color(tui.Bold+tui.White, "✓")
		}
		fmt.Printf("  %s %s%-16s%s %s%s%s\n",
			mark, tui.Bold+tui.White, s.Name, tui.Reset, tui.Gray, s.Detail, tui.Reset)
		if s.Fix != "" {
			tui.Error("      → " + s.Fix)
		}
	}

	fmt.Println()
	if len(steps) > 0 && steps[len(steps)-1].OK {
		tui.Success("Every Hop Works.")
	} else {
		tui.Warn("The First ✗ Is Where It Breaks.")
	}
	tui.PressEnter()
}
