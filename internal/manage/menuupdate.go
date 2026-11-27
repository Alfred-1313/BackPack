package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/backup"
)

func lockUpdate() (*os.File, error) {
	if err := os.MkdirAll(app.ConfigDir, 0755); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(app.ConfigDir, ".update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another Backpack update is in progress")
		}
		return nil, err
	}
	return lock, nil
}

// ApplyMenuUpdate updates the interactive menu and its embedded web panel.
// Running tunnels use the new binary the next time they start.
func ApplyMenuUpdate(logf func(string)) (bool, error) {
	lock, err := lockUpdate()
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if logf == nil {
		logf = func(string) {}
	}

	tag, err := latestTag()
	if err != nil {
		return false, err
	}
	current := installedVersion()
	if !versionValidRe.MatchString(current) {
		return false, fmt.Errorf("could not determine the installed Backpack version")
	}
	if !newerVersion(tag, current) {
		logf("Already up to date (" + current + ").")
		return false, nil
	}

	work, err := os.MkdirTemp("", "backpack-update-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(work)
	archive, err := downloadVerifiedUpdate(tag, work, logf)
	if err != nil {
		return false, err
	}

	// Stage on the same filesystem as the live binary for an atomic switch.
	stage, err := os.MkdirTemp(filepath.Dir(app.BinPath), ".backpack-update-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	binary := filepath.Join(stage, "backpack")
	if err := extractBinaryTo(archive, binary); err != nil {
		return false, fmt.Errorf("could not extract %s: %w", tag, err)
	}
	if out, err := runBounded(binary, 5*time.Second, "-v"); err != nil || parseVer(versionLine(out)) != parseVer(tag) {
		return false, fmt.Errorf("the downloaded binary did not report version %s", tag)
	}

	logf("Taking a safety snapshot...")
	snap, err := backup.TakeSnapshot("pre-update")
	if err != nil {
		return false, fmt.Errorf("could not take a safety snapshot: %w", err)
	}
	panelActive := IsActive(app.WebUIService)
	logf("Installing " + tag + "...")
	if err := os.Rename(binary, app.BinPath); err != nil {
		return false, err
	}
	if panelActive {
		logf("Restarting the web panel...")
		if err := restartUpdatedPanel(); err != nil {
			logf("Restoring the previous version...")
			old, rollbackErr := os.ReadFile(filepath.Join(snap.Dir, "backpack"))
			if rollbackErr == nil {
				rollbackErr = app.WriteFileAtomic(app.BinPath, old, 0755)
			}
			if rollbackErr == nil {
				rollbackErr = restartUpdatedPanel()
			}
			if rollbackErr != nil {
				return false, fmt.Errorf("panel restart failed: %v; rollback failed: %w (snapshot %s)", err, rollbackErr, snap.Dir)
			}
			return false, fmt.Errorf("panel restart failed: %w (previous version restored)", err)
		}
	}
	logf("Installed " + tag + ".")
	return true, nil
}

func restartUpdatedPanel() error {
	if err := RestartService(app.WebUIService); err != nil {
		return err
	}
	if !WaitServiceActive(app.WebUIService, 20*time.Second) {
		return fmt.Errorf("the web panel did not come back up")
	}
	return nil
}
