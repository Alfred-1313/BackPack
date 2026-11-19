package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/backpack/backpack/internal/app"
)

func installerFixture(t *testing.T, body string) {
	t.Helper()
	for _, command := range []string{"git", "bash"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not installed", command)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"add", "install.sh"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "installer fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Only the exact production URL is redirected to the fixture.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+dir+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/Alfred-1313/BackPack.git")
}

func TestTheInstallCommandRunsTheInstaller(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	installerFixture(t, "set -e\n"+
		`test "$BP_BUILD_FROM_SOURCE" = 1`+"\n"+
		"read -r line || true\n"+
		`printf '%s\n' "$line" "$@" > `+quote(marker)+"\n")

	link := `backpack://token'$(exit 7)`
	out, err := exec.Command("sh", "-c", app.SourceInstallCommand("link", "apply", link)+" < /dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("the install line failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\nlink\napply\n" + link + "\n"; string(got) != want {
		t.Fatalf("installer output %q, want %q", got, want)
	}
}

func TestTheUpgradeCommandRestartsTheTunnelsAfterInstalling(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "backpack")
	installerFixture(t, "echo installed >> "+quote(log)+"\n")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"bin $*\" >> "+quote(log)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("sh", "-c", upgradeCommand(app.SourceInstallCommand(), bin)).CombinedOutput()
	if err != nil {
		t.Fatalf("the upgrade line failed: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(log)
	if string(got) != "installed\nbin --restart-all\n" {
		t.Fatalf("the upgrade did %q; want the installer, then --restart-all", got)
	}
}

func TestTheUpgradeCommandStopsWhenTheInstallerFails(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "backpack")
	installerFixture(t, "exit 3\n")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho restarted >> "+quote(log)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := exec.Command("sh", "-c", upgradeCommand(app.SourceInstallCommand(), bin)).Run(); err == nil {
		t.Fatal("a failing installer did not fail the upgrade")
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the tunnels were restarted after the installer failed")
	}
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+filepath.Join(dir, "missing")+".insteadOf")
	if err := exec.Command("sh", "-c", upgradeCommand(app.SourceInstallCommand(), bin)).Run(); err == nil {
		t.Fatal("a failed checkout did not fail the upgrade")
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the tunnels were restarted after the checkout failed")
	}
}
