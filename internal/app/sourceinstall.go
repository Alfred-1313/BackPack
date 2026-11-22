package app

import "strings"

// SourceInstallCommand installs this fork from a temporary source checkout.
// Run it on the destination server as root, with git and bash available.
func SourceInstallCommand(args ...string) string {
	return sourceInstall("", args)
}

// SourceInstallTestCommand installs the optional transport helpers before
// applying a paired Connection Test link.
func SourceInstallTestCommand(link string) string {
	return sourceInstall(" BP_HELPERS=omni,gost", []string{"link", "apply", link})
}

func sourceInstall(helpers string, args []string) string {
	command := `(umask 077; dir=$(mktemp -d) || exit 1; trap 'rm -rf -- "$dir"' EXIT; ` +
		`git clone --depth 1 --branch main ` + SourceURL + `.git "$dir/source" && ` +
		`cd "$dir/source" && BP_BUILD_FROM_SOURCE=1` + helpers + ` bash ./install.sh`
	for _, arg := range args {
		command += " '" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return command + ")"
}
