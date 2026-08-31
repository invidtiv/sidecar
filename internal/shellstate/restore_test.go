package shellstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
)

func writeManifest(t *testing.T, defs ...Definition) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shells.json")
	body, err := json.Marshal(manifest{Version: CurrentVersion, Shells: defs})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readShells(t *testing.T, path string) []Definition {
	t.Helper()
	defs, err := ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

func liveShell(name string) Definition {
	return Definition{
		TmuxName:    name,
		DisplayName: name,
		Namespace:   "/tmp/tmux-test/default",
		WorkDir:     "/repo",
		CreatedAt:   time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC),
	}
}

func idOf(def Definition) Identity {
	return Identity{TmuxName: def.TmuxName, Namespace: def.Namespace}
}

// TestServerDeathPreservesEveryShellRecord is the regression test for the
// incident this work exists to fix: a tmux server dying must never empty
// shells.json.
//
// It drives the write path the liveness surfaces call, once per shell, in the
// exact situation a dead server produces — every session missing at the same
// moment, no server left to ask — and asserts on the file rather than on a
// return value.
func TestServerDeathPreservesEveryShellRecord(t *testing.T) {
	a, b, c := liveShell("sh-a"), liveShell("sh-b"), liveShell("sh-c")
	for _, def := range []*Definition{&a, &b, &c} {
		def.Restore = &RestoreState{Eligible: true, LastSeenServer: "pid=100", LastSeenAliveAt: time.Now().UTC()}
	}
	path := writeManifest(t, a, b, c)

	// No tmux server is running: currentServer is empty for every call.
	for _, def := range []Definition{a, b, c} {
		outcome, err := ForgetOrPreserveAtPath(path, idOf(def), def.CreatedAt, ServerGone())
		if err != nil {
			t.Fatalf("%s: %v", def.TmuxName, err)
		}
		if outcome != ReapPreserved {
			t.Fatalf("%s: outcome %s, want %s", def.TmuxName, outcome, ReapPreserved)
		}
	}

	got := readShells(t, path)
	if len(got) != 3 {
		t.Fatalf("server death emptied the manifest: %d records survive, want 3", len(got))
	}
	tombs, err := ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 0 {
		t.Fatalf("server death must not tombstone anything, got %d tombstones", len(tombs))
	}
	for _, def := range got {
		if def.Restore == nil || !def.Restore.Eligible {
			t.Errorf("%s survived but is not a restore candidate: %+v", def.TmuxName, def.Restore)
		}
		if def.Restore.LastSeenServer != "pid=100" {
			t.Errorf("%s: the departed server id must be kept as the marker, got %q", def.TmuxName, def.Restore.LastSeenServer)
		}
	}
}

// TestReplacedServerPreservesRecords covers the other half: a new server is
// running, but it is not the one these shells were alive in.
func TestReplacedServerPreservesRecords(t *testing.T) {
	a := liveShell("sh-a")
	a.Restore = &RestoreState{Eligible: true, LastSeenServer: "pid=100"}
	path := writeManifest(t, a)

	outcome, err := ForgetOrPreserveAtPath(path, idOf(a), a.CreatedAt, ServerRunning("pid=200"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != ReapPreserved {
		t.Fatalf("outcome %s, want %s", outcome, ReapPreserved)
	}
	if got := readShells(t, path); len(got) != 1 {
		t.Fatalf("a replaced server deleted the record; %d survive", len(got))
	}
}

// TestSingleShellExitInTheSameServerStillTombstones proves the fix did not
// disable reaping. A terminal the user closed, inside a server that is still
// running, is still tombstoned and still recoverable.
func TestSingleShellExitInTheSameServerStillTombstones(t *testing.T) {
	a, b := liveShell("sh-a"), liveShell("sh-b")
	a.Restore = &RestoreState{Eligible: true, LastSeenServer: "pid=100"}
	b.Restore = &RestoreState{Eligible: true, LastSeenServer: "pid=100"}
	path := writeManifest(t, a, b)

	outcome, err := ForgetOrPreserveAtPath(path, idOf(a), a.CreatedAt, ServerRunning("pid=100"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != ReapTombstoned {
		t.Fatalf("outcome %s, want %s", outcome, ReapTombstoned)
	}
	got := readShells(t, path)
	if len(got) != 1 || got[0].TmuxName != "sh-b" {
		t.Fatalf("wrong record removed: %+v", got)
	}
	tombs, err := ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 1 || tombs[0].TmuxName != "sh-a" {
		t.Fatalf("the closed shell must still be recoverable, tombstones: %+v", tombs)
	}
}

// TestTombstoneBranchKeepsTheCreatedAtFence proves the preserve path did not
// quietly drop the fence that stops a reused name from deleting a new record.
func TestTombstoneBranchKeepsTheCreatedAtFence(t *testing.T) {
	a := liveShell("sh-a")
	a.Restore = &RestoreState{Eligible: true, LastSeenServer: "pid=100"}
	path := writeManifest(t, a)

	stale := a.CreatedAt.Add(-time.Hour)
	if _, err := ForgetOrPreserveAtPath(path, idOf(a), stale, ServerRunning("pid=100")); err == nil {
		t.Fatal("a record newer than the observation must be refused")
	}
	if got := readShells(t, path); len(got) != 1 {
		t.Fatal("the refused write must leave the record in place")
	}
}

// TestObserveLiveWritesOnlyOnTransition is the write-amplification guard that
// makes the marker affordable: repeated observation of an unchanged fact must
// not rewrite the file.
func TestObserveLiveWritesOnlyOnTransition(t *testing.T) {
	a := liveShell("sh-a")
	path := writeManifest(t, a)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	changed, err := ObserveLiveAtPath(path, "pid=100", []Identity{idOf(a)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("first observation changed %d records, want 1", changed)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		changed, err := ObserveLiveAtPath(path, "pid=100", []Identity{idOf(a)}, now.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if changed != 0 {
			t.Fatalf("repeat observation %d changed %d records, want 0", i, changed)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("repeated observation of an unchanged fact rewrote shells.json")
	}

	// A new server is a real transition and must be recorded.
	changed, err = ObserveLiveAtPath(path, "pid=200", []Identity{idOf(a)}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("a new server changed %d records, want 1", changed)
	}
	if got := readShells(t, path)[0].Restore.LastSeenServer; got != "pid=200" {
		t.Fatalf("marker %q, want pid=200", got)
	}
}

// TestObserveLiveIgnoresAnUnknownServer pins that "no observation" never becomes
// a marker: a stored eligibility that names no server could never be compared
// against anything later.
func TestObserveLiveIgnoresAnUnknownServer(t *testing.T) {
	a := liveShell("sh-a")
	path := writeManifest(t, a)
	changed, err := ObserveLiveAtPath(path, "", []Identity{idOf(a)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatal("an unobserved server must not produce a marker")
	}
	if readShells(t, path)[0].Restore != nil {
		t.Fatal("no restore state should have been written")
	}
}

func TestSetRestorePolicyRoundTrip(t *testing.T) {
	a := liveShell("sh-a")
	path := writeManifest(t, a)

	if _, err := SetRestorePolicyAtPath(path, idOf(a), agentsession.PolicyResume); err != nil {
		t.Fatal(err)
	}
	if got := RestorePolicyOf(readShells(t, path)[0]); got != agentsession.PolicyResume {
		t.Fatalf("policy %q, want resume", got)
	}

	// Inherit clears the field rather than storing the word, so a record that
	// follows the default serializes no policy at all.
	if _, err := SetRestorePolicyAtPath(path, idOf(a), agentsession.PolicyInherit); err != nil {
		t.Fatal(err)
	}
	got := readShells(t, path)[0]
	if RestorePolicyOf(got) != agentsession.PolicyInherit {
		t.Fatalf("policy %q, want inherit", RestorePolicyOf(got))
	}
	if got.Restore != nil && got.Restore.Policy != "" {
		t.Fatalf("inherit must not be stored as a value: %+v", got.Restore)
	}
}

// TestSetRestorePolicyPreservesTheSessionBinding is the CarryForward hazard seen
// from the other side: a policy write must not be a way to lose a conversation.
func TestSetRestorePolicyPreservesTheSessionBinding(t *testing.T) {
	a := liveShell("sh-a")
	a.Agent = &AgentBinding{Kind: "codex", Session: &agentsession.Ref{
		Kind: agentsession.RefID, Value: "sess-1", Source: "sidecar.codex.hooks", Reported: true,
	}}
	path := writeManifest(t, a)

	if _, err := SetRestorePolicyAtPath(path, idOf(a), agentsession.PolicyNever); err != nil {
		t.Fatal(err)
	}
	got := readShells(t, path)[0]
	if got.Agent == nil || got.Agent.Session == nil || got.Agent.Session.Value != "sess-1" {
		t.Fatalf("the policy write dropped the session binding: %+v", got.Agent)
	}
}

// v2ModelledFields are the Definition fields the workspace plugin's second
// serializer builds from its own in-memory ShellSession. Everything outside this
// set is a field that serializer does not model, and so a field CarryForward is
// responsible for carrying.
//
// The set is written out by name on purpose. If the second serializer ever
// learns a new field, adding it here is a deliberate act with a diff, which is
// exactly the review moment the silent v3 data loss did not get.
var v2ModelledFields = map[string]bool{
	"TmuxName":    true,
	"DisplayName": true,
	"Namespace":   true,
	"CreatedAt":   true,
	"AgentType":   true,
	"SkipPerms":   true,
	"WorkDir":     true,
}

// TestCarryForwardCoversEveryUnmodelledField is the field-drift guard.
//
// CarryForward exists because shells.json has two writers, and the one in the
// workspace plugin replaces a stored record wholesale from a struct that models
// only the v2 fields. When v3 added the agent binding, that writer destroyed it
// on the shell revival path — the cold-restore moment the binding exists to
// serve. The fix was a line in CarryForward per unmodelled field, which is a fix
// that works exactly until someone adds a v4 field and forgets the line.
//
// This test makes forgetting it fail. It fills every field of Definition with a
// distinguishable non-zero value, hands CarryForward a "next" that carries only
// the modelled fields, and requires that no unmodelled field came back zero. A
// new field added to Definition without a corresponding line in CarryForward is
// reported by name.
func TestCarryForwardCoversEveryUnmodelledField(t *testing.T) {
	prior := Definition{}
	fillNonZero(t, reflect.ValueOf(&prior).Elem())

	// next is what the second serializer produces: the modelled fields only.
	next := Definition{}
	priorVal := reflect.ValueOf(prior)
	nextVal := reflect.ValueOf(&next).Elem()
	typ := reflect.TypeOf(Definition{})
	for i := 0; i < typ.NumField(); i++ {
		if v2ModelledFields[typ.Field(i).Name] {
			nextVal.Field(i).Set(priorVal.Field(i))
		}
	}

	got := reflect.ValueOf(CarryForward(prior, next))
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if v2ModelledFields[field.Name] {
			continue
		}
		if got.Field(i).IsZero() {
			t.Errorf("Definition.%s is not modelled by the workspace plugin's serializer and is not carried by "+
				"CarryForward, so a rewrite of an existing record silently destroys it. Add a line to CarryForward "+
				"for it, or add it to v2ModelledFields if that serializer really does write it.", field.Name)
		}
	}
}

// fillNonZero sets every field of a struct to a distinguishable non-zero value,
// allocating pointers and recursing into what they point at, so that "the field
// came back zero" is an unambiguous signal that it was dropped.
func fillNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, elem)
		v.Set(reflect.Append(v, elem))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key := reflect.New(v.Type().Key()).Elem()
		fillNonZero(t, key)
		val := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, val)
		v.SetMapIndex(key, val)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillNonZero(t, v.Elem())
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fillNonZero(t, v.Field(i))
			}
		}
	default:
		t.Fatalf("fillNonZero does not handle %s; extend it so the drift guard keeps working", v.Kind())
	}
}
