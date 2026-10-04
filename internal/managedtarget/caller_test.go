package managedtarget

import (
	"testing"
)

func TestValidateCaller(t *testing.T) {
	for _, tc := range []struct {
		name     string
		e        CallerEvidence
		conflict bool
	}{
		{"stale pane directory", CallerEvidence{ClaimedSession: "a", PaneSession: "a", OriginVerified: true, Cwd: "/projects/b", PaneWorkDir: "/projects/a"}, true},
		{"different pane", CallerEvidence{ClaimedSession: "a", PaneSession: "b", OriginVerified: true, Cwd: "/projects/a", PaneWorkDir: "/projects/a"}, true},
		{"unverifiable claim", CallerEvidence{ClaimedSession: "missing", Cwd: "/projects/a"}, true},
		{"registered cwd contradicts origin without pane", CallerEvidence{ClaimedSession: "a", OriginVerified: true, Cwd: "/projects/b", OriginWorkDir: "/projects/a", RegisteredCwdRoot: "/projects/b"}, true},
		{"genuine cd retains owner", CallerEvidence{ClaimedSession: "a", PaneSession: "a", OriginVerified: true, Cwd: "/projects/b", PaneWorkDir: "/projects/b", OriginWorkDir: "/projects/a", RegisteredCwdRoot: "/projects/b"}, false},
		{"child process directory", CallerEvidence{ClaimedSession: "a", PaneSession: "a", OriginVerified: true, Cwd: "/projects/a/internal", PaneWorkDir: "/projects/a"}, false},
		{"parent process directory is contradiction", CallerEvidence{ClaimedSession: "a", PaneSession: "a", OriginVerified: true, Cwd: "/projects", PaneWorkDir: "/projects/a"}, true},
		{"unregistered directory without pane claims no other workspace", CallerEvidence{ClaimedSession: "a", OriginVerified: true, Cwd: "/tmp/work", OriginWorkDir: "/projects/a"}, false},
		{"no ambient identity", CallerEvidence{Cwd: "/projects/b"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCaller(tc.e)
			if (err != nil) != tc.conflict {
				t.Fatalf("conflict=%v, error=%v", tc.conflict, err)
			}
		})
	}
}
