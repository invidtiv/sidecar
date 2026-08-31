package agentintegration

import (
	"os"
	"strings"
	"testing"
)

// The adapter interface has to describe two structurally different provider
// APIs without either one being the special case. These tests pin the parts of
// that contract a future adapter could quietly break.

// TestEveryAdapterDeclaresWhatItOwnsAtEveryPathItTouches is the interface's
// central invariant. Ownership used to be one boolean with two unwritten
// meanings, decided by whichever adapter happened to set it; anything reading a
// FileState had to know which provider it came from to know what it had been
// told. Declaring it per asset is what makes the surfaces provider-agnostic,
// so an adapter that ships an asset without saying which shape it is has
// reintroduced exactly the ambiguity this replaced.
func TestEveryAdapterDeclaresWhatItOwnsAtEveryPathItTouches(t *testing.T) {
	for _, a := range DefaultAdapters() {
		assets := a.Assets()
		if len(assets) == 0 {
			t.Fatalf("%s ships no assets, so nothing describes what installing it does", a.Provider())
		}
		for _, as := range assets {
			switch as.Ownership {
			case OwnsFile, OwnsEntry:
			default:
				t.Fatalf("%s asset %q declares ownership %q, which is neither shape", a.Provider(), as.Name, as.Ownership)
			}
			if as.Source != a.Source() {
				t.Fatalf("%s asset %q carries source %q, not the adapter's %q", a.Provider(), as.Name, as.Source, a.Source())
			}
			if as.Version == "" {
				t.Fatalf("%s asset %q has no version, so an installed copy can never be called outdated", a.Provider(), as.Name)
			}
			if as.Content == "" {
				t.Fatalf("%s asset %q has no content, so no surface can show what installing it adds", a.Provider(), as.Name)
			}
		}
	}
}

// TestEveryAssetIsAtAPathTheAdapterAlsoReportsOn is what keeps plurality
// honest. Codex edits two files, and while an asset was singular the second one
// was simply undescribed: Assets() answered "hooks.json" and said nothing about
// the config.toml feature flag and trust record without which hooks.json does
// nothing at all. An adapter that grows a third file must grow a third asset,
// and an asset that names a file the adapter never inspects is a description of
// something that does not happen.
func TestEveryAssetIsAtAPathTheAdapterAlsoReportsOn(t *testing.T) {
	env := testEnv(t)
	for _, a := range DefaultAdapters() {
		paths := a.Inspect(env).TargetPaths
		for _, as := range a.Assets() {
			found := false
			for _, p := range paths {
				if strings.HasSuffix(p, "/"+as.Name) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s declares asset %q but reports target paths %v, none of which is it",
					a.Provider(), as.Name, paths)
			}
		}
	}
}

// TestCodexDescribesBothOfTheFilesItEdits is the concrete case the interface
// change exists for, named rather than left to the general rule above. Codex's
// hooks.json is inert without the config.toml feature flag and trust record,
// so an integration that described only the first was describing the half that
// does not work on its own.
func TestCodexDescribesBothOfTheFilesItEdits(t *testing.T) {
	assets := (CodexAdapter{}).Assets()
	if len(assets) != 2 {
		t.Fatalf("codex ships %d assets, want one per file it edits", len(assets))
	}
	names := []string{assets[0].Name, assets[1].Name}
	if names[0] != "hooks.json" || names[1] != "config.toml" {
		t.Fatalf("codex assets are %v, want hooks.json and config.toml", names)
	}
	if !strings.Contains(assets[1].Content, "hooks = true") {
		t.Fatal("the config.toml asset does not show the feature flag, which is the whole reason it exists")
	}
	if !strings.Contains(assets[1].Content, "trusted_hash") {
		t.Fatal("the config.toml asset does not show the trust record Codex refuses to run without")
	}
}

// TestTheMarkerRuleIsNeverRunAgainstAFileTheUserOwns pins the reason the
// ownership decision moved into the asset. Scanning a user's settings.json or
// config.toml for a `// sidecar-integration:` comment could never succeed --
// neither format has that comment syntax -- so an entry adapter always got back
// a FileState claiming it did not own a file it demonstrably had an entry in,
// and then corrected it afterwards. Running the wrong rule and fixing up after
// is the thing that made "owned" mean two things.
//
// The direction that matters is the unsafe one: a marker comment is bytes any
// process can write into a file, so if the marker rule still ran for OwnsEntry
// then pasting one line into a user's settings.json would hand Sidecar the
// whole file -- and uninstall deletes what it owns.
func TestTheMarkerRuleIsNeverRunAgainstAFileTheUserOwns(t *testing.T) {
	env := testEnv(t)
	path := env.Home + "/lookalike.json"
	writeFileT(t, path, "// sidecar-integration: id="+ClaudeSource+" schema=1 version=1\n{}\n")

	entry := (ClaudeAdapter{}).settingsAsset()
	got := inspectFile(env, path, entry)
	if got.Unsafe != "" {
		t.Fatalf("fixture unusable: %s (%s)", got.Unsafe, got.UnsafeDetail)
	}
	if got.Owned {
		t.Fatal("an OwnsEntry asset claimed a whole user-owned file because its bytes carried a marker comment")
	}
	if got.Ownership != OwnsEntry {
		t.Fatalf("inspection did not record the ownership shape it was asked about, got %q", got.Ownership)
	}

	// The same bytes under an OwnsFile asset of the same source are owned,
	// which is what proves the difference is the declared shape rather than
	// anything about the content.
	file := entry
	file.Ownership = OwnsFile
	if got := inspectFile(env, path, file); !got.Owned {
		t.Fatal("an OwnsFile asset did not recognise its own marker")
	}
}

// TestAnOwnedEntryFileCarriesTheShapeASurfaceNeeds is the data half of the
// rendering fix. Codex's config.toml is owned in the entry sense and has no
// version of its own, which rendered as a dangling "sidecar-owned version "
// with nothing after it and told the user Sidecar owned a configuration file
// full of their own settings. The rendering itself is pinned in internal/cli.
func TestAnOwnedEntryFileCarriesTheShapeASurfaceNeeds(t *testing.T) {
	env := testEnv(t)
	writeFileT(t, env.Home+"/.codex/hooks.json", "{}\n")
	writeFileT(t, env.Home+"/.codex/config.toml", "[features]\nhooks = true\n")

	for _, f := range (CodexAdapter{}).Inspect(env).Files {
		if f.Owned && f.Ownership == "" {
			t.Fatalf("%s is owned but does not say in which sense, so no surface can describe it honestly", f.Path)
		}
	}
}

func testEnv(t *testing.T) Env {
	t.Helper()
	return Env{Home: t.TempDir(), UID: os.Getuid()}
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
