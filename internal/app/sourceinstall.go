package app

import "strings"

// SourceInstallCommand installs this fork from a temporary source checkout.
// Run it on the destination server as root, with git and bash available.
func SourceInstallCommand(args ...string) string {
	return sourceInstall("", args)
}

// SourceInstallAnyTLSCommand equips a fresh Kharej server with sing-box.
func SourceInstallAnyTLSCommand(link string) string {
	return sourceInstall(" BP_HELPERS=anytls", []string{"link", "apply", link})
}

// SourceInstallGostCommand equips the peer with the selected GOST helper.
func SourceInstallGostCommand(link string, version int) string {
	helper := "gost"
	if version == 2 {
		helper = "gost-v2"
	}
	return sourceInstall(" BP_HELPERS="+helper, []string{"link", "apply", link})
}

// SourceInstallTestCommand equips a fresh peer for a paired Connection Test.
func SourceInstallTestCommand(link string) string {
	return sourceInstall(" BP_HELPERS=gost,anytls,gost-v2", []string{"link", "apply", link})
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
