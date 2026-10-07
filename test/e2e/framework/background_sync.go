package framework

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	noBackgroundSyncEnv  = "WTP_NO_BACKGROUND_SYNC"
	backgroundSyncWait   = 30 * time.Second
	backgroundSyncPoll   = 50 * time.Millisecond
	backgroundSyncBinDir = "gh-bin"
	fakeGHMode           = 0o755

	// fakeGH answers every `gh pr view` the way gh does for a branch without
	// a pull request, so the refresh completes without network or auth.
	fakeGH = "#!/bin/sh\necho 'no pull requests found for branch' >&2\nexit 1\n"
)

// EnableBackgroundSync lets `wtp list` start its detached refresh in this
// environment, which every other test disables. A fake gh that reports no
// pull requests is put first on PATH, since list only refreshes when gh is
// available and the test must not depend on a real, authenticated gh.
//
// A cleanup waits for the refresh to finish so it cannot write into the
// test's temp dir while that is being removed. It is registered after the
// environment's TempDir, so it runs first.
func (e *TestEnvironment) EnableBackgroundSync() {
	e.t.Helper()

	binDir := filepath.Join(e.tmpDir, backgroundSyncBinDir)
	if err := os.MkdirAll(binDir, dirPerm); err != nil {
		e.t.Fatalf("Failed to create gh bin dir: %v", err)
	}
	ghPath := filepath.Join(binDir, "gh")
	if err := os.WriteFile(ghPath, []byte(fakeGH), fakeGHMode); err != nil {
		e.t.Fatalf("Failed to write fake gh: %v", err)
	}

	env := make([]string, 0, len(e.cmdEnv))
	for _, kv := range e.cmdEnv {
		key, value, _ := strings.Cut(kv, "=")
		switch key {
		case noBackgroundSyncEnv:
			continue
		case "PATH":
			kv = "PATH=" + binDir + string(os.PathListSeparator) + value
		}
		env = append(env, kv)
	}
	e.cmdEnv = env

	e.t.Cleanup(func() {
		if !e.WaitForBackgroundSync() {
			e.t.Errorf("background sync did not finish within %s", backgroundSyncWait)
		}
	})
}

// SyncLockPath returns the lock every wtp sync, including background
// refreshes, holds while it runs.
func (e *TestEnvironment) SyncLockPath() string {
	return filepath.Join(e.xdgDataHome, "wtp", "sync", "lock")
}

// WaitForBackgroundSync waits until a background sync has started and
// released the sync lock. It returns false if none started, or none finished,
// within the timeout. Once the lock is held here, a sync either finished or
// lost the race for the lock and exits without writing anything.
func (e *TestEnvironment) WaitForBackgroundSync() bool {
	e.t.Helper()

	deadline := time.Now().Add(backgroundSyncWait)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e.SyncLockPath()); err == nil {
			fl := flock.New(e.SyncLockPath())
			if locked, lockErr := fl.TryLock(); lockErr == nil && locked {
				_ = fl.Unlock()
				return true
			}
		}
		time.Sleep(backgroundSyncPoll)
	}
	return false
}
