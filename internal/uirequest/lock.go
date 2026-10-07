package uirequest

import (
	"os"
	"path/filepath"
	"syscall"
)

// WithRequestLock orders relay commit/ack against caller cancellation.
func WithRequestLock(dir, id string, action Action, fn func() error) error {
	if _, err := Dir(dir); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "requests", ".relay.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
