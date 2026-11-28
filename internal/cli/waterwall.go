package cli

import (
	"context"
	"fmt"

	"github.com/backpack/backpack/internal/waterwall"
)

func runWaterWall(args []string) Result {
	if len(args) != 2 || args[0] != "update" {
		return fail(CodeUsage, "use backpack waterwall update '<update token>'\n")
	}
	tag, err := waterwall.UpdateTag(args[1])
	if err != nil {
		return fail(CodeUsage, "%s\n", err)
	}
	if !isRoot() {
		return fail(CodeFailed, "run the WaterWall update command with sudo\n")
	}
	asset, err := waterwall.InstallUpdate(context.Background(), tag, false, func(string) {})
	if err != nil {
		return fail(CodeFailed, "could not install WaterWall %s: %v\n", tag, err)
	}
	return ok(fmt.Sprintf("WaterWall %s is ready on Kharej (%s).\nRunning tunnels use it the next time they start.\n", tag, asset))
}
