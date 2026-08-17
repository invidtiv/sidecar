package gitstatus

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

// A chmod-only event must be dropped, or the watcher drives itself: the
// `git status` it triggers touches .git/index's attributes, which the kqueue
// backend reports back as CHMOD. Every other op — including a chmod that
// arrives alongside a real change — must get through.
func TestIsAttributeOnly(t *testing.T) {
	dropped := []fsnotify.Op{fsnotify.Chmod}
	kept := []fsnotify.Op{
		fsnotify.Write,
		fsnotify.Create,
		fsnotify.Remove,
		fsnotify.Rename,
		fsnotify.Create | fsnotify.Chmod,
		fsnotify.Write | fsnotify.Chmod,
	}

	for _, op := range dropped {
		if !isAttributeOnly(op) {
			t.Errorf("isAttributeOnly(%v) = false, want true", op)
		}
	}
	for _, op := range kept {
		if isAttributeOnly(op) {
			t.Errorf("isAttributeOnly(%v) = true, want false", op)
		}
	}
}
