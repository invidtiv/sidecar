package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Same lock file and backoff as td/internal/db.writeLocker so Sidecar notes
// and the td CLI serialize writers on .todos/issues.db.
const (
	dbLockName     = "db.lock"
	dbLockTimeout  = 500 * time.Millisecond
	dbLockBackoff0 = 5 * time.Millisecond
	dbLockBackoff1 = 50 * time.Millisecond
)

func (s *Store) withWriteLock(fn func() error) error {
	return withDBWriteLock(s.dbPath, fn)
}

func withDBWriteLock(dbPath string, fn func() error) error {
	if dbPath == "" {
		return fn()
	}
	lockPath := filepath.Join(filepath.Dir(dbPath), dbLockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer f.Close()

	deadline := time.Now().Add(dbLockTimeout)
	backoff := dbLockBackoff0
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("write lock timeout after %v\n  holder: %s\n  try again or check if holder process is stuck",
				dbLockTimeout, readDBLockHolder(lockPath))
		}
		time.Sleep(backoff)
		if backoff < dbLockBackoff1 {
			backoff *= 2
			if backoff > dbLockBackoff1 {
				backoff = dbLockBackoff1
			}
		}
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	fmt.Fprintf(f, "pid:%d\ntime:%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	_ = f.Sync()

	err = fn()

	_ = f.Truncate(0)
	return err
}

func readDBLockHolder(lockPath string) string {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "unknown"
	}
	var pid, timestamp string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "pid:") {
			pid = strings.TrimPrefix(line, "pid:")
		} else if strings.HasPrefix(line, "time:") {
			timestamp = strings.TrimPrefix(line, "time:")
		}
	}
	if pid == "" {
		return "unknown"
	}
	if n, err := strconv.Atoi(pid); err == nil && !dbLockProcessAlive(n) {
		return fmt.Sprintf("pid:%s since %s (STALE - process dead)", pid, timestamp)
	}
	return fmt.Sprintf("pid:%s since %s", pid, timestamp)
}

func dbLockProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
