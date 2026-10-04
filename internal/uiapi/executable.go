package uiapi

import (
	"context"
	"os"
	"time"
)

// WatchExecutable follows the original launch path, including every symlink.
// Replacements and in-place changes cause a clean server shutdown. A temporarily
// missing path during an upgrade is ignored until its replacement arrives.
func WatchExecutable(ctx context.Context, path string, interval time.Duration) (<-chan struct{}, error) {
	initial, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if interval <= 0 {
		interval = time.Second
	}
	changed := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := os.Stat(path)
				if err == nil && ctx.Err() == nil && (!os.SameFile(initial, current) || initial.Size() != current.Size() || !initial.ModTime().Equal(current.ModTime())) {
					close(changed)
					return
				}
			}
		}
	}()
	return changed, nil
}
