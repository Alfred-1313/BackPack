package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type ctWorkspace struct {
	dir  string
	lock *os.File
	once sync.Once
}

// Hold a lease for the entire run, including while it waits for a peer.
// The registry lock serializes cleanup with the creation of a new lease.
func ctNewWorkspace(temp string) (*ctWorkspace, error) {
	base := filepath.Join(temp, fmt.Sprintf("backpack-conntest-work-%d", os.Geteuid()))
	registry, err := ctLockRegistry(base, true)
	if err != nil {
		return nil, err
	}
	defer registry.Close()
	ctReapWorkspaces(base)
	dir, err := os.MkdirTemp(base, "run-")
	if err != nil {
		return nil, err
	}
	lock, err := os.Open(dir)
	if err == nil {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		if lock != nil {
			lock.Close()
		}
		_ = os.Remove(dir)
		return nil, err
	}
	return &ctWorkspace{dir: dir, lock: lock}, nil
}

func ctLockRegistry(base string, create bool) (*os.File, error) {
	for {
		if create {
			if err := os.Mkdir(base, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		}
		registry, err := os.OpenFile(base, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if create && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		locked, err := registry.Stat()
		if err != nil {
			registry.Close()
			return nil, err
		}
		stat, ok := locked.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || locked.Mode().Perm() != 0700 {
			registry.Close()
			return nil, fmt.Errorf("Connection Test workspace %s must be private to this user", base)
		}
		if err := syscall.Flock(int(registry.Fd()), syscall.LOCK_EX); err != nil {
			registry.Close()
			return nil, err
		}
		current, pathErr := os.Lstat(base)
		if pathErr == nil && os.SameFile(locked, current) {
			return registry, nil
		}
		registry.Close()
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			return nil, pathErr
		}
		// The last run removed this directory while we waited for its lock.
	}
}

func ctReapWorkspaces(base string) {
	entries, _ := os.ReadDir(base)
	for _, entry := range entries {
		id, ok := strings.CutPrefix(entry.Name(), "run-")
		if !ok || !entry.IsDir() {
			continue
		}
		if _, err := strconv.ParseUint(id, 10, 32); err != nil {
			continue
		}
		path := filepath.Join(base, entry.Name())
		lock, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			continue
		}
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			_ = os.RemoveAll(path)
		}
		lock.Close()
	}
}

func (w *ctWorkspace) close() {
	if w != nil {
		w.once.Do(func() {
			_ = os.RemoveAll(w.dir)
			w.lock.Close()
			base := filepath.Dir(w.dir)
			if registry, err := ctLockRegistry(base, false); err == nil {
				_ = os.Remove(base)
				registry.Close()
			}
		})
	}
}
