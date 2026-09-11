package shellstate

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnsureWithAvailableNameConcurrentCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery-sessions.json")
	const count = 12
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			def := Definition{TmuxName: fmt.Sprintf("sidecar-tp-%d", i), DisplayName: "Terminal", Namespace: "/tmp/private"}
			if err := EnsureWithAvailableNameAtPath(path, def); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	defs, err := ListAtPath(path)
	if err != nil || len(defs) != count {
		t.Fatalf("definitions = %v, err = %v", defs, err)
	}
	names := map[string]bool{}
	for _, def := range defs {
		if names[def.DisplayName] {
			t.Fatalf("duplicate name %q", def.DisplayName)
		}
		names[def.DisplayName] = true
	}
	for i := 1; i <= count; i++ {
		name := "Terminal"
		if i > 1 {
			name = fmt.Sprintf("Terminal %d", i)
		}
		if !names[name] {
			t.Errorf("missing available name %q", name)
		}
	}
}

func TestEnsureWithAvailableNamePreservesExistingIdentityAndReusesGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery-sessions.json")
	existing := Definition{TmuxName: "sidecar-tp-existing", DisplayName: "Terminal 3", Namespace: "/tmp/private", CreatedAt: time.Now().UTC(), Restore: &RestoreState{Eligible: true, LastSeenServer: "pid=123"}}
	for _, def := range []Definition{
		{TmuxName: "sidecar-tp-first", DisplayName: "Terminal", Namespace: existing.Namespace}, existing,
	} {
		if err := AddAtPath(path, def); err != nil {
			t.Fatal(err)
		}
	}
	// An earlier record has the requested name; identity must take precedence.
	if err := EnsureWithAvailableNameAtPath(path, Definition{TmuxName: existing.TmuxName, Namespace: existing.Namespace, DisplayName: "Terminal"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWithAvailableNameAtPath(path, Definition{TmuxName: "sidecar-tp-new", Namespace: existing.Namespace, DisplayName: "Terminal"}); err != nil {
		t.Fatal(err)
	}
	defs, err := ListAtPath(path)
	if err != nil || len(defs) != 3 {
		t.Fatalf("definitions = %v, err = %v", defs, err)
	}
	if !reflect.DeepEqual(defs[1], existing) || defs[2].DisplayName != "Terminal 2" {
		t.Fatalf("existing or allocated identity changed: %+v", defs)
	}
}

func TestEnsureWithAvailableNameKeepsNumberedUnicodeNameValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery-sessions.json")
	name := strings.Repeat("界", MaxNameBytes/3)
	for _, session := range []string{"one", "two"} {
		if err := EnsureWithAvailableNameAtPath(path, Definition{TmuxName: session, DisplayName: name}); err != nil {
			t.Fatal(err)
		}
	}
	defs, err := ListAtPath(path)
	if err != nil || len(defs) != 2 {
		t.Fatalf("definitions = %v, err = %v", defs, err)
	}
	if _, err := NormalizeName(defs[1].DisplayName); err != nil || !strings.HasSuffix(defs[1].DisplayName, " 2") {
		t.Fatalf("invalid numbered name %q: %v", defs[1].DisplayName, err)
	}
}
