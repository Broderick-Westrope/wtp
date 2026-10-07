package maintenance

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

const syncFileMode = 0o600

// ErrLocked reports that another sync holds the sync lock.
var ErrLocked = errors.New("another wtp sync is already running")

// SyncDir returns the directory holding sync bookkeeping: the lock, the
// last-run marker, queued notices and the scheduled agent's log.
func SyncDir() string {
	return filepath.Join(xdg.WtpDataDir(), "sync")
}

// LogPath returns the file the scheduled agent writes its output to.
func LogPath() string {
	return filepath.Join(SyncDir(), "sync.log")
}

func lastRunPath() string { return filepath.Join(SyncDir(), "last-run") }
func lockPath() string    { return filepath.Join(SyncDir(), "lock") }
func noticesPath() string { return filepath.Join(SyncDir(), "notices") }
func noticesLock() string { return filepath.Join(SyncDir(), "notices.lock") }

// legacyThrottleDir is where the removed inline maintenance kept per-repo
// throttle timestamps.
func legacyThrottleDir() string {
	return filepath.Join(xdg.WtpDataDir(), "maintenance")
}

// AcquireLock takes the sync lock without blocking. It returns ErrLocked when
// another sync holds it, so overlapping runs (the scheduled agent, a refresh
// started by `wtp list`, a manual `wtp sync`) skip instead of duplicating
// every gh call.
func AcquireLock() (unlock func(), err error) {
	if dirErr := xdg.EnsureDir(SyncDir()); dirErr != nil {
		return nil, dirErr
	}
	fl := flock.New(lockPath())
	ok, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire sync lock: %w", err)
	}
	if !ok {
		return nil, ErrLocked
	}
	return func() { _ = fl.Unlock() }, nil
}

// LastFullSync returns when a sync of every repository last completed, or
// false if none has.
func LastFullSync() (time.Time, bool) {
	info, err := os.Stat(lastRunPath())
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// IsDue reports whether a full sync is due given interval.
func IsDue(interval time.Duration) bool {
	last, ok := LastFullSync()
	return !ok || timeNow().Sub(last) >= interval
}

// MarkFullSync records that a sync of every repository just completed. It
// also removes the throttle files left by the old inline maintenance, which
// nothing reads any more.
func MarkFullSync() error {
	if err := xdg.EnsureDir(SyncDir()); err != nil {
		return err
	}
	now := timeNow()
	if err := os.WriteFile(lastRunPath(), nil, syncFileMode); err != nil {
		return fmt.Errorf("write last-run marker: %w", err)
	}
	if err := os.Chtimes(lastRunPath(), now, now); err != nil {
		return fmt.Errorf("touch last-run marker: %w", err)
	}
	_ = os.RemoveAll(legacyThrottleDir())
	return nil
}

// QueueNotices appends lines for the next interactive command to print. The
// scheduled agent has no terminal, so this is how its auto-archives reach the
// user.
func QueueNotices(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	if err := xdg.EnsureDir(SyncDir()); err != nil {
		return err
	}
	fl := flock.New(noticesLock())
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire notices lock: %w", err)
	}
	defer fl.Unlock() //nolint:errcheck

	f, err := os.OpenFile(noticesPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, syncFileMode)
	if err != nil {
		return fmt.Errorf("open notices: %w", err)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(f, line); err != nil {
			_ = f.Close()
			return fmt.Errorf("write notices: %w", err)
		}
	}
	return f.Close()
}

// DrainNotices returns and clears queued notices. With nothing queued it costs
// a single stat, which is why it is safe to call before every command.
func DrainNotices() ([]string, error) {
	if _, err := os.Stat(noticesPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	fl := flock.New(noticesLock())
	if err := fl.Lock(); err != nil {
		return nil, fmt.Errorf("acquire notices lock: %w", err)
	}
	defer fl.Unlock() //nolint:errcheck

	f, err := os.Open(noticesPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	_ = f.Close()
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read notices: %w", err)
	}
	if err := os.Remove(noticesPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clear notices: %w", err)
	}
	return lines, nil
}
